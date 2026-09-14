package jobs

import (
	"context"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type AuditActor struct {
	ID        ir.ID
	RequestID string
}
type auditActorKey struct{}

// WithAuditActor carries authenticated management metadata to the atomic job
// mutation. Caller-supplied reason text never enters the audit event.
func WithAuditActor(ctx context.Context, actor AuditActor) context.Context {
	return context.WithValue(ctx, auditActorKey{}, actor)
}
func AuditActorFrom(ctx context.Context) AuditActor {
	actor, _ := ctx.Value(auditActorKey{}).(AuditActor)
	return actor
}
