// Package imports defines the local preview/confirmation boundary. Raw input
// and candidates are temporary, secret-bearing records, never catalog nodes.
package imports

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	MaxInputBytes = 10 << 20
	MaxCandidates = 5000
	MaxPageSize   = 200
	Retention     = 7 * 24 * time.Hour
)

var (
	ErrInvalidInput        = errors.New("imports: invalid input")
	ErrUnsupported         = errors.New("imports: unsupported operation")
	ErrNotFound            = errors.New("imports: not found")
	ErrRevisionConflict    = errors.New("imports: revision conflict")
	ErrIdempotencyConflict = errors.New("imports: idempotency conflict")
	ErrStateConflict       = errors.New("imports: state conflict")
	ErrExpired             = errors.New("imports: expired")
	ErrUnavailable         = errors.New("imports: unavailable")
)

type CreateInput struct {
	ScopeID        ir.ID
	PrincipalID    ir.ID
	InputFormat    string
	Text           []byte
	IdempotencyKey string
}

type Accepted struct {
	BatchID  ir.ID                `json:"batch_id"`
	JobID    ir.ID                `json:"job_id"`
	Revision apicontract.Revision `json:"revision"`
	State    string               `json:"state"`
	Replayed bool                 `json:"-"`
}

type Candidate struct {
	CandidateID        ir.ID                 `json:"candidate_id"`
	Index              int                   `json:"index"`
	State              string                `json:"state"`
	Name               string                `json:"name,omitempty"`
	Node               *apicontract.NodeRead `json:"node,omitempty"`
	ExistingResourceID ir.ID                 `json:"existing_resource_id,omitempty"`
	ExistingRevision   apicontract.Revision  `json:"existing_revision,omitempty"`
	MatchMethod        string                `json:"match_method,omitempty"`
	Diagnostics        ir.Diagnostics        `json:"diagnostics"`
}

type PageOptions struct {
	Limit  int
	Cursor string
}

type Batch struct {
	BatchID        ir.ID                `json:"batch_id"`
	Revision       apicontract.Revision `json:"revision"`
	JobID          ir.ID                `json:"job_id"`
	State          string               `json:"state"`
	CandidateCount int                  `json:"candidate_count"`
	Candidates     []Candidate          `json:"candidates"`
	CreatedAt      time.Time            `json:"created_at"`
	ExpiresAt      time.Time            `json:"expires_at"`
	Diagnostics    ir.Diagnostics       `json:"diagnostics"`
	NextCursor     string               `json:"-"`
}

type Decision struct {
	CandidateID      ir.ID                         `json:"candidate_id"`
	Action           string                        `json:"action"`
	ResourceID       ir.ID                         `json:"resource_id,omitempty"`
	ExpectedRevision apicontract.Revision          `json:"expected_revision,omitempty"`
	Override         *apicontract.NodePatchRequest `json:"override,omitempty"`
}

type CommitInput struct {
	ScopeID          ir.ID
	PrincipalID      ir.ID
	BatchID          ir.ID
	ExpectedRevision int64
	IdempotencyKey   string
	RequestID        string
	Decisions        []Decision
}

type CommitItem struct {
	CandidateID ir.ID                `json:"candidate_id"`
	Status      string               `json:"status"`
	ResourceID  ir.ID                `json:"resource_id,omitempty"`
	Revision    apicontract.Revision `json:"revision,omitempty"`
}

// Commit is a typed replay receipt. It cannot carry node payloads or raw text.
type Commit struct {
	BatchID  ir.ID                `json:"batch_id"`
	Revision apicontract.Revision `json:"revision"`
	Items    []CommitItem         `json:"items"`
	Replayed bool                 `json:"-"`
}

type Repository interface {
	Create(context.Context, CreateInput) (Accepted, error)
	Get(context.Context, ir.ID, ir.ID, PageOptions) (Batch, error)
	Commit(context.Context, CommitInput) (Commit, error)
}

func (CreateInput) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (CreateInput) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }
func (CommitInput) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (CommitInput) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }
func (Decision) Format(s fmt.State, _ rune)    { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (Decision) LogValue() slog.Value          { return slog.StringValue("[REDACTED]") }
