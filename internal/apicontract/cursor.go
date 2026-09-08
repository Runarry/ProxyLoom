package apicontract

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const DefaultLimit = 50
const MaxLimit = 200
const MaxCursorBytes = 2048
const CreatedAtIDSort = "created_at,id"

type Pagination struct {
	Limit  int
	Cursor string
}

func ParsePagination(values url.Values) (Pagination, error) {
	out := Pagination{Limit: DefaultLimit}
	if values.Has("limit") {
		if len(values["limit"]) != 1 {
			return Pagination{}, NewError(MalformedRequest)
		}
		raw := values.Get("limit")
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > MaxLimit || strconv.Itoa(limit) != raw {
			return Pagination{}, NewError(MalformedRequest)
		}
		out.Limit = limit
	}
	if values.Has("cursor") {
		if len(values["cursor"]) != 1 || len(values.Get("cursor")) == 0 || len(values.Get("cursor")) > MaxCursorBytes {
			return Pagination{}, NewError(MalformedRequest)
		}
		out.Cursor = values.Get("cursor")
	}
	return out, nil
}

// CursorMAC is a purpose-specific capability, not a master-key or database
// interface. Its implementation must use a separate cursor key/domain.
type CursorMAC interface{ MACCursor([]byte) ([]byte, error) }

type cursorHMAC struct{ key [32]byte }

func (cursorHMAC) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (cursorHMAC) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

// NewCursorHMAC copies a dedicated 32-byte cursor key. The caller must not reuse
// a subscription-token verifier or the global encryption master key here.
func NewCursorHMAC(key []byte) (CursorMAC, error) {
	if len(key) != 32 {
		return nil, NewError(InternalError)
	}
	out := &cursorHMAC{}
	copy(out.key[:], key)
	return out, nil
}

func (m *cursorHMAC) MACCursor(data []byte) ([]byte, error) {
	digest := hmac.New(sha256.New, m.key[:])
	_, _ = digest.Write([]byte("proxyloom/api/cursor/v1\x00"))
	_, _ = digest.Write(data)
	return digest.Sum(nil), nil
}

type CursorBinding struct {
	ScopeID    ir.ID
	Collection string
	Sort       string
	// FilterHash must be produced from the normalized, effective filters. Never
	// include limit/cursor: changing page size must not change query identity.
	FilterHash string
}

type CursorPosition struct {
	CreatedAt time.Time
	ID        ir.ID
}
type CursorCodec struct{ mac CursorMAC }

func (CursorCodec) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (CursorCodec) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

func NewCursorCodec(mac CursorMAC) (*CursorCodec, error) {
	if mac == nil {
		return nil, NewError(InternalError)
	}
	return &CursorCodec{mac: mac}, nil
}

var collectionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}(?:/[a-z][a-z0-9_-]{0,63})?$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FilterHash normalizes query parameter ordering and set-valued filter ordering.
// Handlers must first validate their collection's allowed filters and effective
// defaults; passing raw query values is not an authorization decision.
func FilterHash(filters url.Values) (string, error) {
	canonical := url.Values{}
	for name, values := range filters {
		if name == "cursor" || name == "limit" || !collectionPattern.MatchString(name) || len(values) > 200 {
			return "", NewError(MalformedRequest)
		}
		copyValues := slices.Clone(values)
		for _, value := range copyValues {
			if len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
				return "", NewError(MalformedRequest)
			}
		}
		slices.Sort(copyValues)
		canonical[name] = slices.Compact(copyValues)
	}
	encoded := canonical.Encode()
	if len(encoded) > 16384 {
		return "", NewError(MalformedRequest)
	}
	digest := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(digest[:]), nil
}

func validBinding(binding CursorBinding) bool {
	return binding.ScopeID.Validate() == nil && collectionPattern.MatchString(binding.Collection) && binding.Sort == CreatedAtIDSort && digestPattern.MatchString(binding.FilterHash)
}

type cursorWire struct {
	Version    int    `json:"v"`
	ScopeID    ir.ID  `json:"scope_id"`
	Collection string `json:"collection"`
	Sort       string `json:"sort"`
	FilterHash string `json:"filter_hash"`
	CreatedAt  string `json:"created_at"`
	ID         ir.ID  `json:"id"`
}

func (codec *CursorCodec) Encode(binding CursorBinding, position CursorPosition) (string, error) {
	if codec == nil || codec.mac == nil || !validBinding(binding) || position.ID.Validate() != nil || position.CreatedAt.IsZero() || position.CreatedAt.Year() < 1 || position.CreatedAt.Year() > 9999 {
		return "", NewError(InternalError)
	}
	wire := cursorWire{1, binding.ScopeID, binding.Collection, binding.Sort, binding.FilterHash, position.CreatedAt.UTC().Format(time.RFC3339Nano), position.ID}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", NewError(InternalError)
	}
	mac, err := codec.mac.MACCursor(payload)
	if err != nil || len(mac) != sha256.Size {
		return "", NewError(ServiceUnavailable)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

func (codec *CursorCodec) Decode(token string, binding CursorBinding) (CursorPosition, error) {
	invalid := func() (CursorPosition, error) { return CursorPosition{}, NewError(MalformedRequest) }
	if codec == nil || codec.mac == nil || !validBinding(binding) {
		return CursorPosition{}, NewError(InternalError)
	}
	if len(token) == 0 || len(token) > MaxCursorBytes {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return invalid()
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return invalid()
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(mac) != sha256.Size {
		return invalid()
	}
	expected, err := codec.mac.MACCursor(payload)
	if err != nil || len(expected) != sha256.Size {
		return CursorPosition{}, NewError(ServiceUnavailable)
	}
	if !hmac.Equal(mac, expected) {
		return invalid()
	}
	// MAC verification precedes parsing. Require canonical bytes so a trusted
	// signer cannot accidentally introduce duplicate or unknown cursor fields.
	var wire cursorWire
	if json.Unmarshal(payload, &wire) != nil {
		return invalid()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || string(canonical) != string(payload) {
		return invalid()
	}
	if wire.Version != 1 || wire.ScopeID != binding.ScopeID || wire.Collection != binding.Collection || wire.Sort != binding.Sort || wire.FilterHash != binding.FilterHash || wire.ID.Validate() != nil {
		return invalid()
	}
	createdAt, err := time.Parse(time.RFC3339Nano, wire.CreatedAt)
	if err != nil || createdAt.IsZero() || createdAt.UTC().Format(time.RFC3339Nano) != wire.CreatedAt {
		return invalid()
	}
	return CursorPosition{CreatedAt: createdAt, ID: wire.ID}, nil
}
