package apicontract

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const NodeRevisionSort = "revision"
const NodeReferenceSort = "source_id,revision,path"

type nodeCursor struct {
	Version    int    `json:"v"`
	ScopeID    ir.ID  `json:"scope_id"`
	Collection string `json:"collection"`
	Sort       string `json:"sort"`
	FilterHash string `json:"filter_hash"`
	Revision   string `json:"revision"`
	SourceID   ir.ID  `json:"source_id,omitempty"`
	Path       string `json:"path,omitempty"`
}

func validNodeCursorBinding(binding CursorBinding, collection, sort string) bool {
	return binding.ScopeID.Validate() == nil && binding.Collection == collection && binding.Sort == sort && digestPattern.MatchString(binding.FilterHash)
}

func (codec *CursorCodec) encodeNodeCursor(binding CursorBinding, revision int64, source ir.ID, path string) (string, error) {
	if codec == nil || codec.mac == nil || revision < 1 {
		return "", NewError(InternalError)
	}
	wire := nodeCursor{1, binding.ScopeID, binding.Collection, binding.Sort, binding.FilterHash, strconv.FormatInt(revision, 10), source, path}
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

func (codec *CursorCodec) decodeNodeCursor(token string, binding CursorBinding) (nodeCursor, int64, error) {
	invalid := func() (nodeCursor, int64, error) { return nodeCursor{}, 0, NewError(MalformedRequest) }
	if codec == nil || codec.mac == nil {
		return nodeCursor{}, 0, NewError(InternalError)
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
		return nodeCursor{}, 0, NewError(ServiceUnavailable)
	}
	if !hmac.Equal(mac, expected) {
		return invalid()
	}
	var wire nodeCursor
	if json.Unmarshal(payload, &wire) != nil {
		return invalid()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || string(canonical) != string(payload) || wire.Version != 1 || wire.ScopeID != binding.ScopeID || wire.Collection != binding.Collection || wire.Sort != binding.Sort || wire.FilterHash != binding.FilterHash {
		return invalid()
	}
	revision, err := strconv.ParseInt(wire.Revision, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != wire.Revision {
		return invalid()
	}
	return wire, revision, nil
}

func (codec *CursorCodec) EncodeNodeRevision(binding CursorBinding, revision int64) (string, error) {
	if !validNodeCursorBinding(binding, "nodes/revisions", NodeRevisionSort) {
		return "", NewError(InternalError)
	}
	return codec.encodeNodeCursor(binding, revision, "", "")
}

func (codec *CursorCodec) DecodeNodeRevision(token string, binding CursorBinding) (int64, error) {
	if !validNodeCursorBinding(binding, "nodes/revisions", NodeRevisionSort) {
		return 0, NewError(InternalError)
	}
	wire, revision, err := codec.decodeNodeCursor(token, binding)
	if err != nil {
		return 0, err
	}
	if wire.SourceID != "" || wire.Path != "" {
		return 0, NewError(MalformedRequest)
	}
	return revision, nil
}

func (codec *CursorCodec) EncodeNodeReference(binding CursorBinding, position catalog.ReferencePosition) (string, error) {
	if !validNodeCursorBinding(binding, "nodes/references", NodeReferenceSort) || position.SourceID.Validate() != nil || !validNodeReferencePath(position.Path) {
		return "", NewError(InternalError)
	}
	return codec.encodeNodeCursor(binding, position.SourceRevision, position.SourceID, position.Path)
}

func (codec *CursorCodec) DecodeNodeReference(token string, binding CursorBinding) (catalog.ReferencePosition, error) {
	if !validNodeCursorBinding(binding, "nodes/references", NodeReferenceSort) {
		return catalog.ReferencePosition{}, NewError(InternalError)
	}
	wire, revision, err := codec.decodeNodeCursor(token, binding)
	if err != nil {
		return catalog.ReferencePosition{}, err
	}
	if wire.SourceID.Validate() != nil || !validNodeReferencePath(wire.Path) {
		return catalog.ReferencePosition{}, NewError(MalformedRequest)
	}
	return catalog.ReferencePosition{SourceID: wire.SourceID, SourceRevision: revision, Path: wire.Path}, nil
}

// These are immutable catalog IR paths, not request-derived error pointers.
// P0 has exactly two concrete chain hops; no arbitrary path enters a cursor.
func validNodeReferencePath(path string) bool {
	return path == "/payload/hops/0/node_id" || path == "/payload/hops/1/node_id"
}
