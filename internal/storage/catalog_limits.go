package storage

import (
	"context"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/operations"
)

func limitDiagnostic(path string, id ir.ID) error {
	return ir.Diagnostics{{Code: ir.InputLimitExceeded, Severity: ir.SeverityError, FieldPath: path, ResourceID: id, Message: "The configured workspace limit was exceeded."}}
}
func (t *catalogTx) operationalLimits(ctx context.Context) (operations.CatalogLimits, error) {
	if t.limits == nil {
		s, err := readSystemSettings(ctx, t.tx, t.scope)
		if err != nil {
			return operations.CatalogLimits{}, err
		}
		t.limits = &s.CatalogLimits
	}
	return *t.limits, nil
}
func (t *catalogTx) checkOperationalLimits(ctx context.Context, r ir.Resource, creating bool) error {
	limits, err := t.operationalLimits(ctx)
	if err != nil {
		return err
	}
	if creating && r.Metadata.Kind == ir.KindNode {
		if t.nodeCount == nil {
			var count int
			if err = t.tx.QueryRow(ctx, `SELECT count(*) FROM public.resources WHERE scope_id=$1 AND kind='node' AND deleted_at IS NULL`, dbID(t.scope)).Scan(&count); err != nil {
				return err
			}
			t.nodeCount = &count
		}
		if *t.nodeCount >= limits.MaxNodes {
			return limitDiagnostic("/node", r.Metadata.ResourceID)
		}
		// Scope serialization and the transaction failure latch keep this count
		// exact across a bulk import without rescanning the growing node table.
		*t.nodeCount++
	}
	var rules int
	switch value := r.Payload.(type) {
	case *ir.RuleSet:
		rules = len(value.Entries)
	case *ir.RoutingProfile:
		rules = len(value.Rules)
	case *ir.DNSProfile:
		rules = len(value.Rules)
	case *ir.SubscriptionProfile:
		if len(value.Targets) > limits.MaxTargetsPerSubscription {
			return limitDiagnostic("/subscription/targets", r.Metadata.ResourceID)
		}
	}
	if rules > limits.MaxRules {
		return limitDiagnostic("/payload/rules", r.Metadata.ResourceID)
	}
	return nil
}
