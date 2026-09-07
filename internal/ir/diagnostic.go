package ir

import (
	"fmt"
	"sort"
	"strings"
)

type DiagnosticCode string
type Severity string

const (
	SeverityError            Severity       = "error"
	SeverityWarning          Severity       = "warning"
	SeverityInfo             Severity       = "info"
	InvalidJSON              DiagnosticCode = "IR_INVALID_JSON"
	DuplicateField           DiagnosticCode = "IR_DUPLICATE_FIELD"
	UnknownField             DiagnosticCode = "IR_UNKNOWN_FIELD"
	RequiredField            DiagnosticCode = "IR_REQUIRED"
	InvalidType              DiagnosticCode = "IR_INVALID_TYPE"
	InvalidValue             DiagnosticCode = "IR_INVALID_VALUE"
	UnsupportedVersion       DiagnosticCode = "IR_UNSUPPORTED_VERSION"
	InvalidUnion             DiagnosticCode = "IR_INVALID_UNION"
	InvalidSnapshot          DiagnosticCode = "IR_INVALID_SNAPSHOT"
	DuplicateResource        DiagnosticCode = "IR_DUPLICATE_RESOURCE"
	DuplicateTarget          DiagnosticCode = "IR_DUPLICATE_TARGET"
	ReferenceMissing         DiagnosticCode = "IR_REFERENCE_MISSING"
	ReferenceKind            DiagnosticCode = "IR_REFERENCE_KIND"
	ReferenceRevision        DiagnosticCode = "IR_REFERENCE_REVISION"
	ReferenceEpoch           DiagnosticCode = "IR_REFERENCE_EPOCH"
	ScopeMismatch            DiagnosticCode = "IR_SCOPE_MISMATCH"
	ResourceDisabled         DiagnosticCode = "IR_RESOURCE_DISABLED"
	UnreachableResource      DiagnosticCode = "IR_UNREACHABLE_RESOURCE"
	CapabilityUnsupported    DiagnosticCode = "CAPABILITY_UNSUPPORTED"
	CapabilityUnverified     DiagnosticCode = "CAPABILITY_UNVERIFIED"
	CompileTargetMismatch    DiagnosticCode = "COMPILE_TARGET_MISMATCH"
	CompileUnknownBuild      DiagnosticCode = "COMPILE_UNKNOWN_BUILD"
	CompileDigestMismatch    DiagnosticCode = "COMPILE_DIGEST_MISMATCH"
	CompileAdapterVersion    DiagnosticCode = "COMPILE_ADAPTER_VERSION"
	CompileFormatMismatch    DiagnosticCode = "COMPILE_FORMAT_MISMATCH"
	CompileLabelCollision    DiagnosticCode = "COMPILE_LABEL_COLLISION"
	CompileUnknownCapability DiagnosticCode = "COMPILE_UNKNOWN_CAPABILITY"
)

// FieldPath is an RFC 6901 JSON Pointer (the root is ""). Messages never
// interpolate authentication, endpoints, native configuration, or input values.
type Diagnostic struct {
	Code            DiagnosticCode `json:"code"`
	Severity        Severity       `json:"severity"`
	ResourceID      ID             `json:"resource_id,omitempty"`
	FieldPath       string         `json:"field_path"`
	TargetKey       string         `json:"target_key,omitempty"`
	Message         string         `json:"message"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
}

type Diagnostics []Diagnostic

func (d Diagnostics) Error() string {
	if len(d) == 0 {
		return "IR validation failed"
	}
	return fmt.Sprintf("IR validation failed: %s at %s", d[0].Code, d[0].FieldPath)
}

func issue(code DiagnosticCode, path string) Diagnostic {
	return Diagnostic{Code: code, Severity: SeverityError, FieldPath: path, Message: diagnosticMessage(code)}
}

func (c DiagnosticCode) Message() string { return diagnosticMessage(c) }

func diagnosticMessage(code DiagnosticCode) string {
	switch code {
	case InvalidJSON:
		return "A single valid UTF-8 JSON document is required."
	case DuplicateField:
		return "Duplicate object fields are forbidden."
	case UnknownField:
		return "This field is not part of the v1 contract."
	case RequiredField:
		return "A required field is missing."
	case InvalidType:
		return "The field has an invalid JSON type."
	case UnsupportedVersion:
		return "Only IR schema version 1 is accepted."
	case InvalidUnion:
		return "The discriminator and variant must describe exactly one supported type."
	case InvalidSnapshot:
		return "A validated, fully pinned frozen input is required."
	case DuplicateResource:
		return "A resource ID must occur exactly once."
	case DuplicateTarget:
		return "Each frozen target key must be unique."
	case ReferenceMissing:
		return "The referenced resource is absent from the snapshot."
	case ReferenceKind:
		return "The referenced resource has the wrong kind."
	case ReferenceRevision:
		return "The reference does not match the frozen resource revision."
	case ReferenceEpoch:
		return "The reference does not match the frozen security epoch."
	case ScopeMismatch:
		return "All frozen resources must belong to the snapshot scope."
	case ResourceDisabled:
		return "Disabled resources cannot enter a compile snapshot."
	case UnreachableResource:
		return "The snapshot contains a resource outside the member dependency closure."
	case CapabilityUnsupported:
		return "This capability is not adapted for the target and cannot be substituted."
	case CapabilityUnverified:
		return "This capability is locked but not verified; the skeleton records it and does not mark it verified."
	case CompileTargetMismatch:
		return "The compile target must equal the frozen target with the same key."
	case CompileUnknownBuild:
		return "The frozen core build is not in the locked catalog."
	case CompileDigestMismatch:
		return "The frozen core digest does not match the locked build."
	case CompileAdapterVersion:
		return "The frozen adapter version does not match the compiler skeleton."
	case CompileFormatMismatch:
		return "The frozen output format does not match the core family."
	case CompileLabelCollision:
		return "Deterministic labels could not be made unique without a random suffix."
	case CompileUnknownCapability:
		return "The node combination is not in the locked P0 capability list."
	default:
		return "The value does not satisfy the v1 contract."
	}
}

func pointer(tokens ...string) string {
	var out strings.Builder
	for _, token := range tokens {
		out.WriteByte('/')
		out.WriteString(strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
	}
	return out.String()
}

func stableDiagnostics(d Diagnostics) Diagnostics {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].FieldPath != d[j].FieldPath {
			return d[i].FieldPath < d[j].FieldPath
		}
		return d[i].Code < d[j].Code
	})
	out := make(Diagnostics, 0, len(d))
	for _, item := range d {
		if len(out) > 0 && out[len(out)-1] == item {
			continue
		}
		out = append(out, item)
	}
	return out
}
