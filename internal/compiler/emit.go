package compiler

import (
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/adapter/mihomo"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func emitNative(graph Graph) (adapter.Artifact, []ir.Diagnostic, error) {
	input := adapter.EmitInput{
		SnapshotID:   graph.SnapshotID,
		TargetKey:    graph.Target.Key,
		Independents: graph.Independents,
		Chains:       graph.Chains,
	}
	switch graph.Target.CoreFamily {
	case ir.Xray:
		return xray.Emit(input)
	case ir.SingBox:
		return singbox.Emit(input)
	case ir.Mihomo:
		return mihomo.Emit(input)
	default:
		d := compileIssue(ir.CompileFormatMismatch, "/core_family", graph.Target.Key, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
}
