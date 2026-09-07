package compiler

import (
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func transportKind(transport ir.Transport) ir.TransportKind {
	switch value := transport.(type) {
	case *ir.NativeTCPTransport:
		if value != nil {
			return value.Kind
		}
	case *ir.WebSocketTransport:
		if value != nil {
			return value.Kind
		}
	}
	return ""
}

func securityMode(security ir.Security) ir.SecurityMode {
	switch value := security.(type) {
	case *ir.NoSecurity:
		if value != nil {
			return value.Mode
		}
	case *ir.TLSSecurity:
		if value != nil {
			return value.Mode
		}
	case *ir.RealitySecurity:
		if value != nil {
			return value.Mode
		}
	}
	return ""
}

func nodeCapabilityKey(node ir.Node, combinations []capability.Combination) (string, bool) {
	protocol := string(node.Protocol)
	transport := string(transportKind(node.Transport))
	security := string(securityMode(node.Security))
	if protocol == "" || transport == "" || security == "" {
		return "", false
	}
	for _, combo := range combinations {
		if combo.Protocol != protocol || combo.Transport != transport {
			continue
		}
		if combo.Security == security {
			return combo.CapabilityKey, true
		}
		if combo.Security == "tls_or_none" && (security == string(ir.TLS) || security == string(ir.SecurityNone)) {
			return combo.CapabilityKey, true
		}
	}
	return "", false
}

func expectedFormat(family ir.CoreFamily) (ir.OutputFormat, bool) {
	switch family {
	case ir.Xray:
		return ir.XrayJSON, true
	case ir.SingBox:
		return ir.SingBoxJSON, true
	case ir.Mihomo:
		return ir.MihomoYAML, true
	default:
		return "", false
	}
}
