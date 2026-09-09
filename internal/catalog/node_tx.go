package catalog

import (
	"context"
	"regexp"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

// AuditedTx keeps source reads, revision checks, writes and their audit record
// under the same scope lock. It never exposes a database handle to callers.
type AuditedTx interface {
	Tx
	Head(context.Context, ir.ID) (ir.Resource, error)
	// Probe reads a head revision without latching not-found. Callers that
	// validate several optional targets must not use Head for missing IDs.
	Probe(context.Context, ir.ID) (ir.Resource, error)
	Audit(context.Context, MutationAudit) error
}

type MutationAction string

const (
	AuditNodeCreate     MutationAction = "node.create"
	AuditNodeUpdate     MutationAction = "node.update"
	AuditNodeDelete     MutationAction = "node.delete"
	AuditNodeClone      MutationAction = "node.clone"
	AuditNodeAddTags    MutationAction = "node.add_tags"
	AuditNodeRemoveTags MutationAction = "node.remove_tags"
	AuditNodeSetEnabled MutationAction = "node.set_enabled"
	AuditImportCommit   MutationAction = "import.commit"
	AuditChainCreate    MutationAction = "chain.create"
	AuditChainUpdate    MutationAction = "chain.update"
	AuditChainDelete    MutationAction = "chain.delete"
	AuditSourceCreate   MutationAction = "source.create"
	AuditSourceUpdate   MutationAction = "source.update"
	AuditSourceDelete   MutationAction = "source.delete"
	AuditSourceRefresh  MutationAction = "source.refresh"
)

// MutationAudit contains only server-selected identifiers and allowlisted
// actions. ObjectID can identify an import batch; no resource payload is admitted.
type MutationAudit struct {
	PrincipalID ir.ID
	ObjectID    ir.ID
	Revision    int64
	RequestID   string
	Action      MutationAction
}

var auditRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (a MutationAudit) Validate() error {
	if a.PrincipalID.Validate() != nil || a.ObjectID.Validate() != nil || a.Revision < 1 || !auditRequestID.MatchString(a.RequestID) {
		return ErrInvalidInput
	}
	switch a.Action {
	case AuditNodeCreate, AuditNodeUpdate, AuditNodeDelete, AuditNodeClone, AuditNodeAddTags, AuditNodeRemoveTags, AuditNodeSetEnabled, AuditImportCommit, AuditChainCreate, AuditChainUpdate, AuditChainDelete, AuditSourceCreate, AuditSourceUpdate, AuditSourceDelete, AuditSourceRefresh:
		return nil
	}
	return ErrInvalidInput
}

type NodeListOptions struct {
	Search   string
	Tag      string
	Protocol ir.Protocol
	Enabled  *bool
	After    *Position
	Limit    int
}

type NodePage struct {
	Items []ir.Resource
	Next  *Position
}

type ChainListOptions struct {
	Tag     string
	Enabled *bool
	After   *Position
	Limit   int
}

type ChainPage struct {
	Items []ir.Resource
	Next  *Position
}

type SourceListOptions struct {
	Tag     string
	Enabled *bool
	After   *Position
	Limit   int
}

type NodeRevisionPage struct {
	Items []ir.Resource
	Next  int64
}

type NodeReferenceOptions struct {
	State string // active (default), historical, or all
	After *ReferencePosition
	Limit int
}
