package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	JSONContentType = "application/json"
	YAMLContentType = "application/yaml"
	InboundTag      = "in"
	BlockTag        = "block"
	LoopbackAddr    = "127.0.0.1"
	KindIndependent = "independent"
	KindChainH1     = "chain_h1"
	KindChainH2     = "chain_h2"
)

type IndependentOutbound struct {
	Tag      string
	Resource ir.Resource
}

type ChainInstance struct {
	ResourceID    ir.ID
	Revision      int64
	TagH1         string
	TagH2         string
	Hop1          ir.Resource
	Hop2          ir.Resource
	FailurePolicy ir.FailurePolicy
}

// EmitInput is the frozen, already-expanded graph handed to a family emitter.
// It may contain credentials and must not be logged.
type EmitInput struct {
	SnapshotID   ir.ID
	TargetKey    string
	Independents []IndependentOutbound
	Chains       []ChainInstance
}

func (EmitInput) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "EmitInput{[REDACTED]}") }
func (EmitInput) LogValue() slog.Value           { return slog.StringValue("EmitInput{[REDACTED]}") }
func (IndependentOutbound) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "IndependentOutbound{[REDACTED]}")
}
func (IndependentOutbound) LogValue() slog.Value {
	return slog.StringValue("IndependentOutbound{[REDACTED]}")
}
func (ChainInstance) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "ChainInstance{[REDACTED]}")
}
func (ChainInstance) LogValue() slog.Value { return slog.StringValue("ChainInstance{[REDACTED]}") }

type OutboundRef struct {
	Tag       string
	Kind      string
	Resource  ir.Resource
	DialerTag string
	FieldPath string
}

func (OutboundRef) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "OutboundRef{[REDACTED]}")
}
func (OutboundRef) LogValue() slog.Value { return slog.StringValue("OutboundRef{[REDACTED]}") }

type TrojanTLS struct {
	Endpoint    ir.Endpoint
	Password    ir.Secret
	ServerName  string
	VerifyCert  bool
	ALPN        []string
	Fingerprint string
	UDPFalse    bool
}

func (TrojanTLS) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "TrojanTLS{[REDACTED]}") }
func (TrojanTLS) LogValue() slog.Value           { return slog.StringValue("TrojanTLS{[REDACTED]}") }

func CompileIssue(code ir.DiagnosticCode, path, targetKey string, resource ir.ID) ir.Diagnostic {
	return ir.Diagnostic{
		Code:       code,
		Severity:   ir.SeverityError,
		FieldPath:  path,
		TargetKey:  targetKey,
		ResourceID: resource,
		Message:    code.Message(),
	}
}

func EncodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func SecretBytes(secret ir.Secret) string { return string(secret) }

func IsReservedTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "in", "block", "direct", "reject", "global", "compatible", "pass", "dns-out":
		return true
	default:
		return false
	}
}

func (in EmitInput) ExitTag() (string, error) {
	if len(in.Chains) > 0 {
		return in.Chains[0].TagH2, nil
	}
	if len(in.Independents) > 0 {
		return in.Independents[0].Tag, nil
	}
	d := CompileIssue(ir.InvalidSnapshot, "/members", in.TargetKey, "")
	return "", ir.Diagnostics{d}
}

func (in EmitInput) OutboundRefs() ([]OutboundRef, error) {
	refs := make([]OutboundRef, 0, len(in.Independents)+len(in.Chains)*2)
	for i, outbound := range in.Independents {
		refs = append(refs, OutboundRef{
			Tag:       outbound.Tag,
			Kind:      KindIndependent,
			Resource:  outbound.Resource,
			FieldPath: "/independents/" + strconv.Itoa(i),
		})
	}
	for i, chain := range in.Chains {
		base := "/chains/" + strconv.Itoa(i)
		refs = append(refs,
			OutboundRef{Tag: chain.TagH1, Kind: KindChainH1, Resource: chain.Hop1, FieldPath: base + "/h1"},
			OutboundRef{Tag: chain.TagH2, Kind: KindChainH2, Resource: chain.Hop2, DialerTag: chain.TagH1, FieldPath: base + "/h2"},
		)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.Tag == "" || IsReservedTag(ref.Tag) {
			d := CompileIssue(ir.CompileLabelCollision, ref.FieldPath, in.TargetKey, ref.Resource.Metadata.ResourceID)
			return nil, ir.Diagnostics{d}
		}
		if _, exists := seen[ref.Tag]; exists {
			d := CompileIssue(ir.CompileLabelCollision, ref.FieldPath, in.TargetKey, ref.Resource.Metadata.ResourceID)
			return nil, ir.Diagnostics{d}
		}
		seen[ref.Tag] = struct{}{}
	}
	return refs, nil
}

func (in EmitInput) OrderedOutboundRefs() (string, []OutboundRef, error) {
	exit, err := in.ExitTag()
	if err != nil {
		return "", nil, err
	}
	refs, err := in.OutboundRefs()
	if err != nil {
		return "", nil, err
	}
	var first OutboundRef
	rest := make([]OutboundRef, 0, len(refs))
	found := false
	for _, ref := range refs {
		if ref.Tag == exit {
			first = ref
			found = true
			continue
		}
		rest = append(rest, ref)
	}
	if !found {
		d := CompileIssue(ir.InvalidSnapshot, "/members", in.TargetKey, "")
		return "", nil, ir.Diagnostics{d}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Tag < rest[j].Tag })
	return exit, append([]OutboundRef{first}, rest...), nil
}

func MapTrojanNativeTLS(resource ir.Resource, path, targetKey string) (TrojanTLS, error) {
	node, ok := resource.Payload.(*ir.Node)
	if !ok || node == nil {
		d := CompileIssue(ir.InvalidUnion, path, targetKey, resource.Metadata.ResourceID)
		return TrojanTLS{}, ir.Diagnostics{d}
	}
	fail := func(code ir.DiagnosticCode, suffix string) error {
		d := CompileIssue(code, path+suffix, targetKey, resource.Metadata.ResourceID)
		return ir.Diagnostics{d}
	}
	if node.Protocol != ir.Trojan {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/protocol")
	}
	auth, ok := node.Auth.(*ir.PasswordAuth)
	if !ok || auth == nil || auth.Kind != ir.AuthPassword {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/auth")
	}
	transport, ok := node.Transport.(*ir.NativeTCPTransport)
	if !ok || transport == nil || transport.Kind != ir.NativeTCP {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/transport")
	}
	tls, ok := node.Security.(*ir.TLSSecurity)
	if !ok || tls == nil || tls.Mode != ir.TLS {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/security")
	}
	if tls.VerifyCertificate == nil {
		return TrojanTLS{}, fail(ir.RequiredField, "/security/verify_certificate")
	}
	if node.Features.Multiplex != nil && *node.Features.Multiplex {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/features/multiplex")
	}
	if node.Features.ProtocolVariant != nil {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/features/protocol_variant")
	}
	if node.Features.UDP != nil && *node.Features.UDP {
		return TrojanTLS{}, fail(ir.CompileUnmappedField, "/features/udp")
	}
	mapped := TrojanTLS{
		Endpoint:   node.Endpoint,
		Password:   auth.Password,
		ServerName: tls.ServerName,
		VerifyCert: *tls.VerifyCertificate,
		ALPN:       slices.Clone(tls.ALPN),
		UDPFalse:   node.Features.UDP != nil && !*node.Features.UDP,
	}
	if tls.ClientFingerprint != nil {
		mapped.Fingerprint = *tls.ClientFingerprint
	}
	return mapped, nil
}
