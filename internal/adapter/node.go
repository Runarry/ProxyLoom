package adapter

import (
	"fmt"
	"log/slog"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

// MappedNode retains every executable Node field. Origin is provenance only;
// Extensions has an empty schema whitelist. Families own native field names.
type MappedNode struct {
	Node     *ir.Node
	Password ir.Secret
	Username ir.Secret
	UUID     ir.Secret
	Cipher   string
	Flow     string
	WS       *ir.WebSocketTransport
	TLS      *ir.TLSSecurity
	Reality  *ir.RealitySecurity
}

func (MappedNode) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "MappedNode{[REDACTED]}") }
func (MappedNode) LogValue() slog.Value       { return slog.StringValue("MappedNode{[REDACTED]}") }

func MapNode(resource ir.Resource, path, target string) (MappedNode, error) {
	fail := func(suffix string) (MappedNode, error) {
		return MappedNode{}, ir.Diagnostics{CompileIssue(ir.CompileUnmappedField, path+suffix, target, resource.Metadata.ResourceID)}
	}
	node, ok := resource.Payload.(*ir.Node)
	if !ok || node == nil {
		return fail("")
	}
	if err := node.Validate(); err != nil {
		diagnostics := err.(ir.Diagnostics)
		for i := range diagnostics {
			diagnostics[i].FieldPath = path + diagnostics[i].FieldPath
			diagnostics[i].TargetKey = target
			diagnostics[i].ResourceID = resource.Metadata.ResourceID
		}
		return MappedNode{}, diagnostics
	}
	if node.Features.UDP != nil && *node.Features.UDP {
		return fail("/features/udp")
	}
	if node.Features.Multiplex != nil && *node.Features.Multiplex {
		return fail("/features/multiplex")
	}
	m := MappedNode{Node: node}
	switch auth := node.Auth.(type) {
	case *ir.MethodPasswordAuth:
		m.Password, m.Cipher = auth.Password, string(auth.Method)
	case *ir.VMessAuth:
		m.UUID, m.Cipher = auth.UUID, string(auth.Cipher)
	case *ir.UUIDAuth:
		m.UUID = auth.UUID
	case *ir.PasswordAuth:
		m.Password = auth.Password
	case *ir.UsernamePasswordAuth:
		m.Username, m.Password = auth.Username, auth.Password
	case *ir.NoAuth:
	default:
		return fail("/auth")
	}
	switch transport := node.Transport.(type) {
	case *ir.NativeTCPTransport:
	case *ir.WebSocketTransport:
		m.WS = transport
	default:
		return fail("/transport")
	}
	switch security := node.Security.(type) {
	case *ir.NoSecurity:
	case *ir.TLSSecurity:
		m.TLS = security
	case *ir.RealitySecurity:
		m.Reality = security
	default:
		return fail("/security")
	}
	if node.Features.ProtocolVariant != nil {
		m.Flow = string(*node.Features.ProtocolVariant)
	}
	return m, nil
}
