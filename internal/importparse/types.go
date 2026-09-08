// Package importparse converts the documented share-link dialects into typed
// IR. It does not fetch URLs, execute plugins, persist candidates, or infer node
// identity. Candidate JSON contains secrets and requires encrypted storage.
package importparse

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	FormatAuto   = "auto"
	FormatText   = "text"
	FormatBase64 = "base64"

	StatusValid       = "valid"
	StatusInvalid     = "invalid"
	StatusUnsupported = "unsupported"

	MaxDecodedBytes = 10 * 1024 * 1024
	MaxEntries      = 5000
	MaxURIBytes     = 16 * 1024
	MaxBase64Layers = 2
	// Bound even encoded input before copying or removing whitespace. This is
	// the padded representation of the maximum decoded size after two layers.
	MaxInputBytes = ((MaxDecodedBytes+2)/3*4 + 2) / 3 * 4
)

const (
	InvalidFormat   ir.DiagnosticCode = "IMPORT_INVALID_FORMAT"
	EmptyInput      ir.DiagnosticCode = "IMPORT_EMPTY_INPUT"
	InputTooLarge   ir.DiagnosticCode = "IMPORT_INPUT_TOO_LARGE"
	DecodedTooLarge ir.DiagnosticCode = "IMPORT_DECODED_TOO_LARGE"
	TooManyEntries  ir.DiagnosticCode = "IMPORT_TOO_MANY_ENTRIES"
	URITooLarge     ir.DiagnosticCode = "IMPORT_URI_TOO_LARGE"
	TooManyLayers   ir.DiagnosticCode = "IMPORT_TOO_MANY_BASE64_LAYERS"
	InvalidBase64   ir.DiagnosticCode = "IMPORT_INVALID_BASE64"
	InvalidUTF8     ir.DiagnosticCode = "IMPORT_INVALID_UTF8"
	NativeConfig    ir.DiagnosticCode = "IMPORT_NATIVE_CONFIG_UNSUPPORTED"
	InvalidURI      ir.DiagnosticCode = "IMPORT_INVALID_URI"
	UnknownScheme   ir.DiagnosticCode = "IMPORT_UNKNOWN_SCHEME"
	DuplicateField  ir.DiagnosticCode = "IMPORT_DUPLICATE_FIELD"
	RequiredField   ir.DiagnosticCode = "IMPORT_REQUIRED_FIELD"
	InvalidValue    ir.DiagnosticCode = "IMPORT_INVALID_VALUE"
	Unsupported     ir.DiagnosticCode = "IMPORT_UNSUPPORTED_PARAMETER"
	UnknownMetadata ir.DiagnosticCode = "IMPORT_UNKNOWN_METADATA"
	UnsafeTLS       ir.DiagnosticCode = "IMPORT_UNSAFE_TLS"
	Ambiguous       ir.DiagnosticCode = "IMPORT_AMBIGUOUS_PARAMETER"
)

// Metadata contains isolated, potentially sensitive upstream fields. Values
// are JSON strings for query parameters, and original JSON values for VMess.
// It is never merged into Node.Extensions or emitted as native configuration.
type Metadata map[string]json.RawMessage

type Candidate struct {
	Index       int            `json:"index"`
	Line        int            `json:"line"`
	Name        string         `json:"name"`
	Dialect     string         `json:"dialect"`
	Status      string         `json:"status"`
	Node        *ir.Node       `json:"node,omitempty"`
	Diagnostics ir.Diagnostics `json:"diagnostics"`
	Metadata    Metadata       `json:"metadata,omitempty"`
}

func (c Candidate) Valid() bool { return c.Status == StatusValid && c.Node != nil }

type Result struct {
	Format       string      `json:"format"`
	Base64Layers int         `json:"base64_layers"`
	Candidates   []Candidate `json:"candidates"`
}

func (Candidate) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (Candidate) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }
func (Result) String() string                { return "[REDACTED]" }
func (Result) GoString() string              { return "[REDACTED]" }
func (Result) LogValue() slog.Value          { return slog.StringValue("[REDACTED]") }
func (Metadata) Format(s fmt.State, _ rune)  { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (Metadata) LogValue() slog.Value        { return slog.StringValue("[REDACTED]") }

// CanonicalConnection returns sensitive, deterministic fingerprint input.
// Caller supplies a domain-separated HMAC; these bytes must never be logged.
// Names, IDs, revisions live outside Node, and Origin is deliberately excluded.
func CanonicalConnection(node ir.Node) ([]byte, error) {
	if err := node.Validate(); err != nil {
		return nil, err
	}
	node.Origin = nil
	return json.Marshal(node)
}

func issue(code ir.DiagnosticCode, path string) ir.Diagnostic {
	message := "The value does not satisfy the documented import dialect."
	switch code {
	case InvalidFormat:
		message = "Choose auto, text, or base64 input format."
	case EmptyInput:
		message = "At least one nonblank share-link entry is required."
	case InputTooLarge:
		message = "The encoded input exceeds the import byte budget."
	case DecodedTooLarge:
		message = "Decoded input exceeds the 10 MiB budget."
	case TooManyEntries:
		message = "An import may contain at most 5000 nonblank entries."
	case URITooLarge:
		message = "A share link may contain at most 16 KiB."
	case TooManyLayers:
		message = "At most two Base64 list envelopes are allowed."
	case InvalidBase64:
		message = "A strict standard or URL-safe Base64 list is required."
	case InvalidUTF8:
		message = "Valid UTF-8 text is required."
	case NativeConfig:
		message = "Native JSON, YAML, and compressed configurations cannot be imported in P0."
	case InvalidURI:
		message = "A share link in a documented URI dialect is required."
	case UnknownScheme:
		message = "This share-link scheme is not supported."
	case DuplicateField:
		message = "Duplicate query or JSON fields are forbidden."
	case RequiredField:
		message = "A required connection or authentication field is missing."
	case Unsupported:
		message = "This connection parameter cannot be represented without changing its meaning."
	case UnknownMetadata:
		message = "Upstream metadata is isolated and is not part of the executable node."
	case UnsafeTLS:
		message = "Imported links cannot disable certificate verification."
	case Ambiguous:
		message = "Conflicting or ambiguous connection parameters are forbidden."
	}
	return ir.Diagnostic{Code: code, Severity: ir.SeverityError, FieldPath: path, Message: message}
}

func failure(code ir.DiagnosticCode, path string) error {
	return ir.Diagnostics{issue(code, path)}
}

func (c *Candidate) fail(code ir.DiagnosticCode, path string) {
	c.Node = nil
	c.Status = StatusInvalid
	if code == Unsupported || code == UnknownScheme || code == NativeConfig {
		c.Status = StatusUnsupported
	}
	c.Diagnostics = append(c.Diagnostics, issue(code, path))
}

func (c *Candidate) metadata(key string, value json.RawMessage) {
	if c.Metadata == nil {
		c.Metadata = make(Metadata)
	}
	c.Metadata[key] = append(json.RawMessage(nil), value...)
}

func (c *Candidate) warnMetadata() {
	for _, d := range c.Diagnostics {
		if d.Code == UnknownMetadata {
			return
		}
	}
	d := issue(UnknownMetadata, "/metadata")
	d.Severity = ir.SeverityWarning
	c.Diagnostics = append(c.Diagnostics, d)
}
