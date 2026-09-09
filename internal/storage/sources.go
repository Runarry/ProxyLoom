package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/safefetch"
	"github.com/Runarry/ProxyLoom/internal/source"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
)

type Sources struct {
	catalog *Catalog
	jobs    *Jobs
	fetch   *safefetch.Client
}

func NewSources(store *Catalog, queue *Jobs, fetch *safefetch.Client) (*Sources, error) {
	if store == nil || queue == nil {
		return nil, catalog.ErrInvalidInput
	}
	if fetch == nil {
		fetch = &safefetch.Client{}
	}
	return &Sources{catalog: store, jobs: queue, fetch: fetch}, nil
}

func (s *Sources) Head(ctx context.Context, scope, id ir.ID) (source.Document, error) {
	return s.catalog.SourceHead(ctx, scope, id)
}

func (s *Sources) List(ctx context.Context, scope ir.ID, options catalog.SourceListOptions) (source.Page, error) {
	return s.catalog.ListSources(ctx, scope, options)
}

func (s *Sources) Items(ctx context.Context, scope, sourceID ir.ID) ([]override.Item, error) {
	return s.catalog.SourceItems(ctx, scope, sourceID)
}

func (s *Sources) Create(ctx context.Context, input source.Mutation) (source.Document, error) {
	if !validIDs(input.ScopeID, input.PrincipalID) || input.Config.Validate() != nil {
		return source.Document{}, catalog.ErrInvalidInput
	}
	var document source.Document
	err := s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		var err error
		document, err = t.CreateSource(ctx, input.Name, input.Tags, input.Enabled, input.Config)
		if err != nil {
			return err
		}
		if err := t.Audit(ctx, catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: document.Metadata.ResourceID,
			Revision: document.Metadata.Revision, RequestID: input.RequestID, Action: catalog.AuditSourceCreate}); err != nil {
			return err
		}
		return s.syncSchedule(ctx, t, document, false, "", nil, time.Now().UTC())
	})
	return document, err
}

func (s *Sources) Update(ctx context.Context, input source.Mutation) (source.Document, error) {
	if !validIDs(input.ScopeID, input.PrincipalID, input.ResourceID) || input.ExpectedRevision < 1 || input.Config.Validate() != nil {
		return source.Document{}, catalog.ErrInvalidInput
	}
	var document source.Document
	err := s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		var err error
		document, err = t.UpdateSource(ctx, input.ResourceID, input.ExpectedRevision, input.Name, input.Tags, input.Enabled, input.Config)
		if err != nil {
			return err
		}
		if err := t.Audit(ctx, catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: document.Metadata.ResourceID,
			Revision: document.Metadata.Revision, RequestID: input.RequestID, Action: catalog.AuditSourceUpdate}); err != nil {
			return err
		}
		return s.syncSchedule(ctx, t, document, false, "", nil, time.Now().UTC())
	})
	return document, err
}

func (s *Sources) Delete(ctx context.Context, input source.Mutation) (source.Document, error) {
	if !validIDs(input.ScopeID, input.PrincipalID, input.ResourceID) || input.ExpectedRevision < 1 {
		return source.Document{}, catalog.ErrInvalidInput
	}
	var document source.Document
	err := s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		var err error
		document, err = t.DeleteSource(ctx, input.ResourceID, input.ExpectedRevision)
		if err != nil {
			return err
		}
		if err := t.Audit(ctx, catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: document.Metadata.ResourceID,
			Revision: document.Metadata.Revision, RequestID: input.RequestID, Action: catalog.AuditSourceDelete}); err != nil {
			return err
		}
		return s.syncSchedule(ctx, t, document, true, "", nil, time.Now().UTC())
	})
	return document, err
}

type refreshPayload struct {
	SourceID  ir.ID  `json:"source_id"`
	Revision  int64  `json:"revision"`
	ActorID   ir.ID  `json:"actor_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func (s *Sources) EnqueueRefresh(ctx context.Context, input source.RefreshRequest) (jobs.Job, bool, error) {
	if !validIDs(input.ScopeID, input.SourceID) || input.ExpectedRevision < 1 {
		return jobs.Job{}, false, catalog.ErrInvalidInput
	}
	if input.IdempotencyKey != "" {
		if input.PrincipalID.Validate() != nil {
			return jobs.Job{}, false, catalog.ErrInvalidInput
		}
		canonical, err := json.Marshal(struct {
			Source   ir.ID `json:"source_id"`
			Revision int64 `json:"revision"`
		}{input.SourceID, input.ExpectedRevision})
		if err != nil {
			return jobs.Job{}, false, catalog.ErrInvalidInput
		}
		var job jobs.Job
		result, err := s.catalog.ExecuteIdempotent(ctx, catalog.IdempotencyRequest{ScopeID: input.ScopeID, PrincipalID: input.PrincipalID,
			RouteKey: "sources.refresh", Key: input.IdempotencyKey, CanonicalRequest: canonical}, func(tx catalog.Tx) (catalog.Receipt, error) {
			t, ok := tx.(*catalogTx)
			if !ok {
				return catalog.Receipt{}, catalog.ErrUnavailable
			}
			var err error
			job, err = s.enqueue(ctx, t, input)
			if err != nil {
				return catalog.Receipt{}, err
			}
			return catalog.Receipt{HTTPStatus: 202, ResourceID: input.SourceID, Revision: input.ExpectedRevision, OperationID: job.ID, Status: catalog.ReceiptAccepted}, nil
		})
		if err != nil {
			return jobs.Job{}, false, err
		}
		if result.Replayed {
			job, err = s.jobs.Get(ctx, input.ScopeID, result.Receipt.OperationID)
			return job, true, err
		}
		return job, false, nil
	}
	var job jobs.Job
	err := s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		var err error
		job, err = s.enqueue(ctx, t, input)
		return err
	})
	return job, false, err
}

func (s *Sources) enqueue(ctx context.Context, t *catalogTx, input source.RefreshRequest) (jobs.Job, error) {
	document, err := t.sourceHead(ctx, input.SourceID, true)
	if err != nil {
		return jobs.Job{}, err
	}
	if document.Metadata.Revision != input.ExpectedRevision {
		return jobs.Job{}, catalog.ErrRevisionConflict
	}
	var active bool
	if err := t.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.jobs WHERE scope_id=$1 AND batch_id=$2 AND type='source_refresh' AND state IN ('queued','leased','running'))`,
		dbID(input.ScopeID), dbID(input.SourceID)).Scan(&active); err != nil {
		return jobs.Job{}, err
	}
	if active {
		return jobs.Job{}, jobs.ErrConflict
	}
	framed, err := json.Marshal(refreshPayload{SourceID: input.SourceID, Revision: input.ExpectedRevision, ActorID: input.PrincipalID, RequestID: input.RequestID})
	if err != nil {
		return jobs.Job{}, catalog.ErrInvalidInput
	}
	defer clear(framed)
	job, err := s.jobs.EnqueueTx(ctx, t.tx, jobs.EnqueueInput{ScopeID: input.ScopeID, BatchID: input.SourceID, Executor: jobs.APIWorker, Type: jobs.SourceRefresh, Payload: framed})
	if err != nil {
		return jobs.Job{}, err
	}
	if input.PrincipalID.Validate() == nil && input.RequestID != "" {
		if err := t.Audit(ctx, catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: input.SourceID,
			Revision: document.Metadata.Revision, RequestID: input.RequestID, Action: catalog.AuditSourceRefresh}); err != nil {
			return jobs.Job{}, err
		}
	}
	return job, nil
}

func (s *Sources) ScheduleDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 200 {
		return 0, catalog.ErrInvalidInput
	}
	rows, err := s.catalog.q.ListDueSourceSchedules(ctx, int32(limit))
	if err != nil {
		return 0, catalogError(err)
	}
	enqueued := 0
	for _, row := range rows {
		scope, id := irID(row.ScopeID), irID(row.SourceID)
		revision := int64(0)
		if row.HeadRevision.Valid {
			revision = row.HeadRevision.Int64
		}
		if revision < 1 {
			continue
		}
		_, _, err := s.EnqueueRefresh(ctx, source.RefreshRequest{ScopeID: scope, SourceID: id, ExpectedRevision: revision, RequestID: "schedule"})
		if err == nil {
			enqueued++
			continue
		}
		if errors.Is(err, jobs.ErrConflict) || errors.Is(err, catalog.ErrNotFound) || errors.Is(err, catalog.ErrRevisionConflict) {
			continue
		}
		return enqueued, err
	}
	return enqueued, nil
}

func attachCatalogTx(store *Catalog, tx pgx.Tx, scope ir.ID) *catalogTx {
	return &catalogTx{store: store, tx: tx, q: dbgen.New(tx), scope: scope, active: true}
}
