package compiler

import (
	"context"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type Compiler struct {
	catalog *capability.Catalog
	labels  labeler
}

func New(catalog *capability.Catalog) *Compiler {
	if catalog == nil {
		return nil
	}
	return &Compiler{catalog: catalog, labels: defaultLabeler()}
}

func Load() (*Compiler, error) {
	catalog, err := capability.Load()
	if err != nil {
		return nil, err
	}
	return New(catalog), nil
}

func (c *Compiler) Compile(ctx context.Context, input ir.FrozenInput, target ir.Target) (adapter.Artifact, []ir.Diagnostic, error) {
	if c == nil || c.catalog == nil {
		d := compileIssue(ir.InvalidSnapshot, "", "", "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return adapter.Artifact{}, nil, err
		}
	}
	graph, diags, err := c.Prepare(input, target)
	if err != nil {
		return adapter.Artifact{}, diags, err
	}
	payload, err := marshalPlan(BuildPlan(graph))
	if err != nil {
		d := compileIssue(ir.InvalidSnapshot, "", target.Key, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	return adapter.Artifact{
		SnapshotID:  graph.SnapshotID,
		TargetKey:   target.Key,
		ContentType: PlanContentType,
		Bytes:       payload,
	}, diags, nil
}
