package catalog

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestRoutingReferenceClosureBoundsMissingLookupsAndDiagnostics(t *testing.T) {
	const scope ir.ID = "10000000-0000-4000-8000-000000000001"
	refs := make([]Reference, 2001)
	for i := range refs {
		refs[i] = Reference{TargetID: ir.ID(fmt.Sprintf("20000000-0000-4000-8000-%012d", i)), ExpectedKind: ir.KindRuleSet, Path: "/rules/0/match/rule_set_ids/0"}
	}
	reads := 0
	resolve := func(ir.ID) (ir.Resource, error) { reads++; return ir.Resource{}, ErrNotFound }
	if _, err := ResolveTargetReferences(scope, refs, resolve); !errors.Is(err, ErrInvalidInput) || reads != 2000 {
		t.Fatalf("lookup budget not enforced: reads=%d error=%v", reads, err)
	}
	reads = 0
	for i := range refs {
		refs[i] = refs[0]
	}
	_, err := ResolveTargetReferences(scope, refs, resolve)
	var diagnostics ir.Diagnostics
	if !errors.As(err, &diagnostics) || len(diagnostics) != 16 || reads != 1 {
		t.Fatalf("repeated missing refs amplified reads or diagnostics: reads=%d error=%v", reads, err)
	}
}
