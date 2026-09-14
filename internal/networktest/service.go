package networktest

import (
	"context"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"time"
)

type Actor struct {
	ScopeID, ID ir.ID
	Key         string
}
type Subject struct {
	Kind ir.ResourceKind `json:"kind"`
	ID   ir.ID           `json:"id"`
}
type Request struct {
	Subjects     []Subject             `json:"subjects"`
	CoreBuildID  ir.ID                 `json:"core_build_id"`
	Type         string                `json:"type"`
	TestTargetID ir.ID                 `json:"test_target_id,omitempty"`
	Limits       runnerprotocol.Limits `json:"limits"`
}
type Repository interface {
	Targets(context.Context, ir.ID, int, PagePosition, *bool) ([]Target, bool, error)
	Target(context.Context, ir.ID, ir.ID) (Target, error)
	WriteTarget(context.Context, Actor, ir.ID, int64, TargetRequest) (Target, error)
	DeleteTarget(context.Context, Actor, ir.ID, int64) error
	Create(context.Context, Actor, Request) (ir.ID, error)
	Results(context.Context, ir.ID, ResultFilter) ([]json.RawMessage, bool, error)
}
type ResultFilter struct {
	SubjectID, CoreBuildID      ir.ID
	After                       PagePosition
	SubjectRevision             int64
	Type, Location, From, Until string
	Limit                       int
}

type PagePosition struct {
	ID        ir.ID
	CreatedAt time.Time
}
