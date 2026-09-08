// Package catalog defines resource persistence contracts and revision semantics.
// It has no database or encryption dependencies.
package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

var (
	ErrNotFound            = errors.New("catalog: not found")
	ErrRevisionConflict    = errors.New("catalog: revision conflict")
	ErrInvalidReference    = errors.New("catalog: invalid reference")
	ErrInvalidInput        = errors.New("catalog: invalid input")
	ErrUnavailable         = errors.New("catalog: unavailable")
	ErrCrypto              = errors.New("catalog: encryption failure")
	ErrIdempotencyConflict = errors.New("catalog: idempotency conflict")
	ErrWrapConflict        = errors.New("catalog: wrapping conflict")
	ErrTransactionClosed   = errors.New("catalog: transaction closed")
)

// Inputs contain complete replacements, after the API resolves typed patches.
type CreateInput struct {
	Name    string
	Tags    []string
	Enabled bool
	Payload ir.ResourcePayload
}

type UpdateInput struct {
	Name    string
	Tags    []string
	Enabled bool
	Payload ir.ResourcePayload
}

// Tx is valid only during its repository callback. Implementations enforce
// expected revisions and commit the scope revision once per business transaction.
type Tx interface {
	Create(context.Context, CreateInput) (ir.Resource, error)
	Update(context.Context, ir.ID, int64, UpdateInput) (ir.Resource, error)
	Delete(context.Context, ir.ID, int64) (ir.Resource, error)
	Revoke(context.Context, ir.ID, int64) (ir.Resource, error)
}

type Repository interface {
	Head(context.Context, ir.ID, ir.ID) (ir.Resource, error)
	Revision(context.Context, ir.ID, ir.ID, int64) (ir.Resource, error)
	Transact(context.Context, ir.ID, func(Tx) error) error
	ExecuteIdempotent(context.Context, IdempotencyRequest, func(Tx) (Receipt, error)) (IdempotencyResult, error)
}

type IdempotencyRequest struct {
	ScopeID          ir.ID
	PrincipalID      ir.ID
	RouteKey         string
	Key              string
	CanonicalRequest []byte
}

type ReceiptStatus string

const (
	ReceiptCreated  ReceiptStatus = "created"
	ReceiptUpdated  ReceiptStatus = "updated"
	ReceiptDeleted  ReceiptStatus = "deleted"
	ReceiptRevoked  ReceiptStatus = "revoked"
	ReceiptAccepted ReceiptStatus = "accepted"
)

// Receipt deliberately admits no payload, token, message, or arbitrary body.
type Receipt struct {
	HTTPStatus  int           `json:"http_status"`
	ResourceID  ir.ID         `json:"resource_id,omitempty"`
	Revision    int64         `json:"revision,omitempty"`
	OperationID ir.ID         `json:"operation_id,omitempty"`
	Status      ReceiptStatus `json:"status"`
}

func (r Receipt) Validate() error {
	if (r.ResourceID != "" && r.ResourceID.Validate() != nil) || (r.OperationID != "" && r.OperationID.Validate() != nil) || r.Revision < 0 {
		return ErrInvalidInput
	}
	switch r.Status {
	case ReceiptCreated:
		if r.HTTPStatus != 201 {
			return ErrInvalidInput
		}
	case ReceiptUpdated, ReceiptDeleted, ReceiptRevoked:
		if r.HTTPStatus != 200 && r.HTTPStatus != 204 {
			return ErrInvalidInput
		}
	case ReceiptAccepted:
		if r.HTTPStatus != 202 || r.OperationID == "" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if r.Status != ReceiptAccepted && (r.ResourceID == "" || r.Revision < 1) {
		return ErrInvalidInput
	}
	if (r.ResourceID == "") != (r.Revision == 0) {
		return ErrInvalidInput
	}
	return nil
}

func (r Receipt) Valid() bool { return r.Validate() == nil }

type IdempotencyResult struct {
	Receipt  Receipt
	Replayed bool
}
type Summary struct {
	Metadata  ir.Metadata
	CreatedAt time.Time
	DeletedAt *time.Time
}
type ListOptions struct {
	Kind           ir.ResourceKind
	Tag            string
	IncludeDeleted bool
	After          *Position
	Limit          int
}
type Position struct {
	CreatedAt time.Time
	ID        ir.ID
}
type Page struct {
	Items []Summary
	Next  *Position
}
type TagOptions struct {
	After string
	Limit int
}
type TagPage struct {
	Items []string
	Next  string
}
type Reference struct {
	SourceID       ir.ID
	SourceKind     ir.ResourceKind
	SourceRevision int64
	TargetID       ir.ID
	TargetRevision *int64
	ExpectedKind   ir.ResourceKind
	Path           string
	Current        bool
}
type ReferenceOptions struct {
	IncludeHistorical bool
	After             *ReferencePosition
	Limit             int
}
type ReferencePosition struct {
	SourceID       ir.ID
	SourceRevision int64
	Path           string
}
type ReferencePage struct {
	Items []Reference
	Next  *ReferencePosition
}
type Scope struct {
	ID              ir.ID
	Name            string
	CatalogRevision int64
	AuthEpoch       int64
}
