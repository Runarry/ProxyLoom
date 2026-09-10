package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/Runarry/ProxyLoom/internal/source"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newResourceID() (ir.ID, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", catalog.ErrUnavailable
	}
	id[6] = (id[6] & 15) | 64
	id[8] = (id[8] & 63) | 128
	return ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])), nil
}

func (c *Catalog) openSource(row dbgen.GetResourceRevisionRow) (source.Document, error) {
	var payload secretbox.Payload
	var wrapping secretbox.Wrapping
	if json.Unmarshal(row.Envelope, &payload) != nil || json.Unmarshal(row.Wrapping, &wrapping) != nil {
		return source.Document{}, catalog.ErrCrypto
	}
	aad := recordContext(row)
	plain, err := c.box.Open(aad, payload, wrapping)
	if err != nil {
		return source.Document{}, catalog.ErrCrypto
	}
	defer clear(plain)
	var document source.Document
	if json.Unmarshal(plain, &document) != nil {
		return source.Document{}, catalog.ErrCrypto
	}
	if document.Metadata.ScopeID != aad.ScopeID || document.Metadata.ResourceID != aad.ObjectID ||
		document.Metadata.Revision != aad.Revision || document.Metadata.SchemaVersion != aad.SchemaVersion ||
		document.Metadata.Kind != ir.KindSource || document.Metadata.SecurityEpoch != row.SecurityEpoch {
		return source.Document{}, catalog.ErrCrypto
	}
	canonical, err := source.Canonical(document)
	if err != nil {
		return source.Document{}, catalog.ErrCrypto
	}
	defer clear(canonical)
	digest, err := c.box.Digest(secretbox.PurposeResourceContent, canonical)
	if err != nil || !hmac.Equal(digest, row.ContentHmac) {
		return source.Document{}, catalog.ErrCrypto
	}
	return document, nil
}

func (t *catalogTx) writeSource(ctx context.Context, document source.Document, deleted bool) error {
	plain, err := source.Canonical(document)
	if err != nil {
		return catalog.ErrInvalidInput
	}
	defer clear(plain)
	return t.persistPlain(ctx, document.Metadata, plain, nil, deleted)
}

func (t *catalogTx) sourceHead(ctx context.Context, id ir.ID, latchMissing bool) (source.Document, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return source.Document{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return source.Document{}, t.failed
	}
	if id.Validate() != nil {
		t.failed = catalog.ErrInvalidInput
		return source.Document{}, t.failed
	}
	index, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(id)})
	if err != nil {
		mapped := catalogError(err)
		if latchMissing || (mapped != catalog.ErrNotFound && mapped != catalog.ErrInvalidInput) {
			t.failed = mapped
		}
		return source.Document{}, mapped
	}
	if index.DeletedAt.Valid || index.Kind != string(ir.KindSource) {
		if latchMissing {
			t.failed = catalog.ErrNotFound
		}
		return source.Document{}, catalog.ErrNotFound
	}
	row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(id)})
	var document source.Document
	if err == nil {
		document, err = t.store.openSource(dbgen.GetResourceRevisionRow(row))
	}
	if err != nil {
		mapped := catalogError(err)
		if latchMissing || (mapped != catalog.ErrNotFound && mapped != catalog.ErrInvalidInput) {
			t.failed = mapped
		}
		return source.Document{}, mapped
	}
	return document, nil
}

func (t *catalogTx) CreateSource(ctx context.Context, name string, tags []string, enabled bool, cfg source.Config) (source.Document, error) {
	var document source.Document
	err := t.runLocked(func() error {
		id, err := newResourceID()
		if err != nil {
			return err
		}
		if tags == nil {
			tags = []string{}
		}
		document = source.Document{Metadata: ir.Metadata{ResourceID: id, ScopeID: t.scope, Kind: ir.KindSource, Revision: 1,
			SchemaVersion: ir.SchemaVersion, Name: name, Tags: tags, Enabled: enabled, SecurityEpoch: 1}, Source: cfg}
		if err := t.q.InsertResource(ctx, dbgen.InsertResourceParams{ID: dbID(id), ScopeID: dbID(t.scope),
			Kind: string(ir.KindSource), Name: name, Enabled: enabled}); err != nil {
			return err
		}
		if err := t.lockAndCheckRefs(ctx, nil, nil); err != nil {
			return err
		}
		return t.writeSource(ctx, document, false)
	})
	return document, err
}

func nextSource(old source.Document, name string, tags []string, enabled bool, cfg source.Config, deleted bool) (source.Document, error) {
	if old.Metadata.Revision == math.MaxInt64 || old.Metadata.SecurityEpoch == math.MaxInt64 {
		return source.Document{}, catalog.ErrInvalidInput
	}
	if tags == nil {
		tags = []string{}
	}
	next := old
	next.Metadata.Revision++
	next.Metadata.Name, next.Metadata.Tags, next.Metadata.Enabled = name, tags, enabled
	next.Source = cfg
	if deleted {
		next.Metadata.Enabled = false
	}
	if (old.Metadata.Enabled && !next.Metadata.Enabled) || old.Source.URL != cfg.URL || old.Source.Auth != cfg.Auth {
		next.Metadata.SecurityEpoch++
	}
	if _, err := source.Canonical(next); err != nil {
		return source.Document{}, catalog.ErrInvalidInput
	}
	return next, nil
}

func (t *catalogTx) UpdateSource(ctx context.Context, id ir.ID, expected int64, name string, tags []string, enabled bool, cfg source.Config) (source.Document, error) {
	return t.changeSource(ctx, id, expected, false, func(old source.Document) (source.Document, error) {
		return nextSource(old, name, tags, enabled, cfg, false)
	})
}

func (t *catalogTx) DeleteSource(ctx context.Context, id ir.ID, expected int64) (source.Document, error) {
	return t.changeSource(ctx, id, expected, true, func(old source.Document) (source.Document, error) {
		return nextSource(old, old.Metadata.Name, old.Metadata.Tags, false, old.Source, true)
	})
}

func (t *catalogTx) changeSource(ctx context.Context, id ir.ID, expected int64, deleted bool, apply func(source.Document) (source.Document, error)) (source.Document, error) {
	var next source.Document
	err := t.runLocked(func() error {
		if id.Validate() != nil || expected < 1 {
			return catalog.ErrInvalidInput
		}
		row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(id)})
		if err != nil {
			return err
		}
		if row.Revision != expected {
			return catalog.ErrRevisionConflict
		}
		old, err := t.store.openSource(dbgen.GetResourceRevisionRow(row))
		if err != nil {
			return err
		}
		next, err = apply(old)
		if err != nil {
			return err
		}
		if err := t.lockAndCheckRefs(ctx, []ir.ID{id}, nil); err != nil {
			return err
		}
		return t.writeSource(ctx, next, deleted)
	})
	return next, err
}

func (s *Sources) syncSchedule(ctx context.Context, t *catalogTx, document source.Document, deleted bool, job ir.ID, success *bool, now time.Time) error {
	if deleted || !document.Source.RefreshPolicy.Enabled {
		return t.q.DeleteSourceSchedule(ctx, dbgen.DeleteSourceScheduleParams{ScopeID: dbID(t.scope), SourceID: dbID(document.Metadata.ResourceID)})
	}
	interval := document.Source.RefreshPolicy.IntervalSeconds
	backoff := 60
	nextRun := now.Add(time.Duration(interval) * time.Second)
	if success != nil && !*success {
		// The enclosing source transaction holds the scope lock. Read the
		// committed schedule here so consecutive failed refreshes accumulate.
		current, err := t.q.GetSourceScheduleBackoff(ctx, dbgen.GetSourceScheduleBackoffParams{ScopeID: dbID(t.scope), SourceID: dbID(document.Metadata.ResourceID)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			backoff = int(current)
		}
		backoff = nextBackoff(backoff, interval)
		nextRun = now.Add(time.Duration(backoff) * time.Second)
	}
	var last pgtype.UUID
	if job != "" {
		last = dbID(job)
	}
	return t.q.UpsertSourceSchedule(ctx, dbgen.UpsertSourceScheduleParams{SourceID: dbID(document.Metadata.ResourceID),
		ScopeID: dbID(t.scope), NextRunAt: pgtype.Timestamptz{Time: nextRun.UTC(), Valid: true}, BackoffSeconds: int32(backoff), LastJobID: last})
}

func nextBackoff(current, interval int) int {
	next := current * 2
	if next < 60 {
		next = 60
	}
	if next > interval {
		next = interval
	}
	if next > 2592000 {
		next = 2592000
	}
	return next
}

func (s *Sources) sealRecord(scope ir.ID, table string, id ir.ID, revision int64, plain []byte) ([]byte, []byte, []byte, error) {
	payload, wrapping, err := s.catalog.box.Seal(secretbox.Context{ScopeID: scope, Table: table, ObjectID: id, Revision: revision, SchemaVersion: 1}, plain)
	if err != nil {
		return nil, nil, nil, catalog.ErrCrypto
	}
	digest, err := s.catalog.box.Digest(secretbox.PurposeResourceContent, plain)
	if err != nil {
		return nil, nil, nil, catalog.ErrCrypto
	}
	envelope, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, nil, catalog.ErrCrypto
	}
	wrap, err := json.Marshal(wrapping)
	if err != nil {
		return nil, nil, nil, catalog.ErrCrypto
	}
	return envelope, wrap, digest, nil
}

func (s *Sources) openRecord(scope ir.ID, table string, id ir.ID, revision int64, envelope, wrapping, digest []byte) ([]byte, error) {
	var payload secretbox.Payload
	var wrap secretbox.Wrapping
	if json.Unmarshal(envelope, &payload) != nil || json.Unmarshal(wrapping, &wrap) != nil {
		return nil, catalog.ErrCrypto
	}
	plain, err := s.catalog.box.Open(secretbox.Context{ScopeID: scope, Table: table, ObjectID: id, Revision: revision, SchemaVersion: 1}, payload, wrap)
	if err != nil {
		return nil, catalog.ErrCrypto
	}
	if digest != nil {
		sum, err := s.catalog.box.Digest(secretbox.PurposeResourceContent, plain)
		if err != nil || !hmac.Equal(sum, digest) {
			clear(plain)
			return nil, catalog.ErrCrypto
		}
	}
	return plain, nil
}
