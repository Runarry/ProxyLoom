package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/jackc/pgx/v5"
)

type Imports struct {
	catalog *Catalog
	jobs    *Jobs
}

var _ imports.Repository = (*Imports)(nil)

func NewImports(c *Catalog, queue *Jobs) (*Imports, error) {
	if c == nil || queue == nil {
		return nil, imports.ErrInvalidInput
	}
	return &Imports{catalog: c, jobs: queue}, nil
}

func importID() (ir.ID, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", imports.ErrUnavailable
	}
	id[6] = (id[6] & 15) | 64
	id[8] = (id[8] & 63) | 128
	return ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])), nil
}

func validImportKey(key string) bool {
	if len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (s *Imports) Create(ctx context.Context, input imports.CreateInput) (imports.Accepted, error) {
	if !validIDs(input.ScopeID, input.PrincipalID) || len(input.Text) == 0 || len(input.Text) > imports.MaxInputBytes || !utf8.Valid(input.Text) || !validImportKey(input.IdempotencyKey) {
		return imports.Accepted{}, imports.ErrInvalidInput
	}
	switch input.InputFormat {
	case "auto", "uri_list", "base64_uri_list":
	default:
		return imports.Accepted{}, imports.ErrUnsupported
	}
	batch, err := importID()
	if err != nil {
		return imports.Accepted{}, err
	}
	job, err := importID()
	if err != nil {
		return imports.Accepted{}, err
	}
	framed, err := json.Marshal(struct {
		Scope, Actor ir.ID
		Format       string
		Text         []byte
	}{input.ScopeID, input.PrincipalID, input.InputFormat, input.Text})
	if err != nil {
		return imports.Accepted{}, imports.ErrInvalidInput
	}
	defer clear(framed)
	digest, err := s.catalog.box.Digest(secretbox.PurposeImportCommit, framed)
	if err != nil {
		return imports.Accepted{}, imports.ErrUnavailable
	}
	envelope, wrapping, err := s.seal(input.ScopeID, secretbox.TableImportBatches, batch, input.Text)
	if err != nil {
		return imports.Accepted{}, err
	}
	result := imports.Accepted{BatchID: batch, JobID: job, Revision: 1, State: "queued"}
	err = s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		if input.IdempotencyKey != "" {
			prior, replay, err := s.key(ctx, t.tx, input.ScopeID, input.PrincipalID, "create", input.IdempotencyKey, digest, batch)
			if err != nil {
				return err
			}
			if replay {
				if err := t.tx.QueryRow(ctx, "SELECT job_id FROM public.import_batches WHERE scope_id=$1 AND id=$2", dbID(input.ScopeID), dbID(prior)).Scan(&result.JobID); err != nil {
					return err
				}
				result.BatchID = prior
				result.Replayed = true
				return nil
			}
		}
		_, err := t.tx.Exec(ctx, `INSERT INTO public.import_batches(id,scope_id,actor_id,job_id,format,raw_envelope,raw_wrapping)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, dbID(batch), dbID(input.ScopeID), dbID(input.PrincipalID), dbID(job), input.InputFormat, envelope, wrapping)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			BatchID ir.ID `json:"batch_id"`
		}{batch})
		_, err = s.jobs.EnqueueTx(ctx, t.tx, jobs.EnqueueInput{ID: job, ScopeID: input.ScopeID, BatchID: batch, Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: payload})
		return err
	})
	if err != nil {
		return imports.Accepted{}, importError(err)
	}
	return result, nil
}

// key is called with the workspace locked. Its insert and associated business
// mutation commit together; the FK is deferred until a new batch exists.
func (s *Imports) key(ctx context.Context, tx pgx.Tx, scope, actor ir.ID, route, key string, digest []byte, batch ir.ID) (ir.ID, bool, error) {
	var prior ir.ID
	var priorScope ir.ID
	var previous []byte
	err := tx.QueryRow(ctx, "SELECT scope_id,batch_id,request_hmac FROM public.import_request_keys WHERE actor_id=$1 AND route=$2 AND key=$3", dbID(actor), route, key).Scan(&priorScope, &prior, &previous)
	if err == nil {
		if scope != priorScope || !hmac.Equal(previous, digest) {
			return "", false, imports.ErrIdempotencyConflict
		}
		return prior, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO public.import_request_keys(actor_id,route,key,scope_id,request_hmac,batch_id) VALUES($1,$2,$3,$4,$5,$6)", dbID(actor), route, key, dbID(scope), digest, dbID(batch))
	return batch, false, err
}

func (s *Imports) seal(scope ir.ID, table string, id ir.ID, plain []byte) ([]byte, []byte, error) {
	p, w, err := s.catalog.box.Seal(secretbox.Context{ScopeID: scope, Table: table, ObjectID: id, Revision: 1, SchemaVersion: 1}, plain)
	if err != nil {
		return nil, nil, imports.ErrUnavailable
	}
	e, err := json.Marshal(p)
	if err != nil {
		return nil, nil, imports.ErrUnavailable
	}
	wrap, err := json.Marshal(w)
	if err != nil {
		return nil, nil, imports.ErrUnavailable
	}
	return e, wrap, nil
}

func (s *Imports) open(scope ir.ID, table string, id ir.ID, envelope, wrapping []byte) ([]byte, error) {
	var p secretbox.Payload
	var w secretbox.Wrapping
	if json.Unmarshal(envelope, &p) != nil || json.Unmarshal(wrapping, &w) != nil {
		return nil, imports.ErrUnavailable
	}
	plain, err := s.catalog.box.Open(secretbox.Context{ScopeID: scope, Table: table, ObjectID: id, Revision: 1, SchemaVersion: 1}, p, w)
	if err != nil {
		return nil, imports.ErrUnavailable
	}
	return plain, nil
}

type importCursor struct {
	Scope    ir.ID `json:"scope"`
	Batch    ir.ID `json:"batch"`
	Revision int64 `json:"revision"`
	Index    int   `json:"index"`
}

func (s *Imports) encodeCursor(c importCursor) (string, error) {
	plain, _ := json.Marshal(c)
	digest, err := s.catalog.box.Digest(secretbox.PurposeCursor, plain)
	if err != nil {
		return "", imports.ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(plain) + "." + base64.RawURLEncoding.EncodeToString(digest), nil
}

func (s *Imports) decodeCursor(raw string, scope, batch ir.ID, revision int64) (int, error) {
	if len(raw) > 2048 {
		return 0, imports.ErrInvalidInput
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return 0, imports.ErrInvalidInput
	}
	plain, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return 0, imports.ErrInvalidInput
	}
	digest, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return 0, imports.ErrInvalidInput
	}
	want, err := s.catalog.box.Digest(secretbox.PurposeCursor, plain)
	if err != nil {
		return 0, imports.ErrUnavailable
	}
	if !hmac.Equal(digest, want) {
		return 0, imports.ErrInvalidInput
	}
	var c importCursor
	if json.Unmarshal(plain, &c) != nil {
		return 0, imports.ErrInvalidInput
	}
	canonical, _ := json.Marshal(c)
	if string(canonical) != string(plain) || c.Scope != scope || c.Batch != batch || c.Index < 0 || c.Index >= imports.MaxCandidates {
		return 0, imports.ErrInvalidInput
	}
	if c.Revision != revision {
		return 0, imports.ErrRevisionConflict
	}
	return c.Index, nil
}

func (s *Imports) Get(ctx context.Context, scope, batch ir.ID, options imports.PageOptions) (imports.Batch, error) {
	if !validIDs(scope, batch) || options.Limit < 1 || options.Limit > imports.MaxPageSize {
		return imports.Batch{}, imports.ErrInvalidInput
	}
	if err := s.expireBatch(ctx, scope, batch); err != nil {
		return imports.Batch{}, err
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return imports.Batch{}, imports.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	b := imports.Batch{Candidates: []imports.Candidate{}, Diagnostics: ir.Diagnostics{}}
	var diagnostics []byte
	err = tx.QueryRow(ctx, `SELECT b.id,b.revision,b.job_id,
		CASE WHEN b.state='queued' AND j.state IN ('leased','running') THEN 'parsing'
		WHEN b.state='queued' AND j.state IN ('failed','canceled','timed_out') THEN 'failed' ELSE b.state END,
		b.candidate_count,b.created_at,b.expires_at,b.diagnostics
		FROM public.import_batches b JOIN public.jobs j ON j.scope_id=b.scope_id AND j.id=b.job_id
		WHERE b.scope_id=$1 AND b.id=$2`, dbID(scope), dbID(batch)).Scan(&b.BatchID, &b.Revision, &b.JobID, &b.State, &b.CandidateCount, &b.CreatedAt, &b.ExpiresAt, &diagnostics)
	if err != nil {
		return imports.Batch{}, importError(err)
	}
	if json.Unmarshal(diagnostics, &b.Diagnostics) != nil {
		return imports.Batch{}, imports.ErrUnavailable
	}
	if b.State == "failed" && len(b.Diagnostics) == 0 {
		b.Diagnostics = ir.Diagnostics{{Code: "IMPORT_JOB_FAILED", Severity: ir.SeverityError, FieldPath: "/text", Message: "The import parsing job did not complete. Submit a new import to retry."}}
	}
	after := -1
	if options.Cursor != "" {
		after, err = s.decodeCursor(options.Cursor, scope, batch, int64(b.Revision))
		if err != nil {
			return imports.Batch{}, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,ordinal,envelope,wrapping FROM public.import_candidates WHERE scope_id=$1 AND batch_id=$2 AND ordinal>$3 ORDER BY ordinal LIMIT $4`, dbID(scope), dbID(batch), after, options.Limit+1)
	if err != nil {
		return imports.Batch{}, importError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id ir.ID
		var index int
		var envelope, wrapping []byte
		if err := rows.Scan(&id, &index, &envelope, &wrapping); err != nil {
			return imports.Batch{}, importError(err)
		}
		if len(b.Candidates) == options.Limit {
			b.NextCursor, err = s.encodeCursor(importCursor{scope, batch, int64(b.Revision), b.Candidates[len(b.Candidates)-1].Index})
			if err != nil {
				return imports.Batch{}, err
			}
			break
		}
		candidate, err := s.openCandidate(scope, batch, id, index, envelope, wrapping)
		if err != nil {
			return imports.Batch{}, err
		}
		read, err := candidate.read(scope, id, index)
		if err != nil {
			return imports.Batch{}, err
		}
		b.Candidates = append(b.Candidates, read)
	}
	if err := rows.Err(); err != nil {
		return imports.Batch{}, importError(err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return imports.Batch{}, importError(err)
	}
	return b, nil
}

// Candidate details, including arbitrary upstream metadata, never appear in a
// plaintext database column. DecodeNode restores the validated typed unions.
type importCandidate struct {
	BatchID          ir.ID                      `json:"batch_id"`
	Index            int                        `json:"index"`
	Name             string                     `json:"name"`
	State            string                     `json:"state"`
	Node             json.RawMessage            `json:"node,omitempty"`
	Metadata         map[string]json.RawMessage `json:"metadata,omitempty"`
	Diagnostics      ir.Diagnostics             `json:"diagnostics"`
	ExistingID       ir.ID                      `json:"existing_id,omitempty"`
	ExistingRevision int64                      `json:"existing_revision,omitempty"`
	MatchMethod      string                     `json:"match_method,omitempty"`
}

func (importCandidate) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (importCandidate) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

func (s *Imports) openCandidate(scope, batch, id ir.ID, index int, envelope, wrapping []byte) (importCandidate, error) {
	plain, err := s.open(scope, secretbox.TableImportCandidates, id, envelope, wrapping)
	if err != nil {
		return importCandidate{}, err
	}
	defer clear(plain)
	var c importCandidate
	if json.Unmarshal(plain, &c) != nil || c.BatchID != batch || c.Index != index {
		return importCandidate{}, imports.ErrUnavailable
	}
	return c, nil
}

func (c importCandidate) resource(scope, id ir.ID) (ir.Resource, error) {
	if c.State == "invalid" || len(c.Node) == 0 {
		return ir.Resource{}, imports.ErrInvalidInput
	}
	node, err := ir.DecodeNode(c.Node)
	if err != nil {
		return ir.Resource{}, imports.ErrUnavailable
	}
	return ir.Resource{Metadata: ir.Metadata{ScopeID: scope, ResourceID: id, Revision: 1, SchemaVersion: 1, SecurityEpoch: 1, Kind: ir.KindNode, Name: c.Name, Enabled: true, Tags: []string{}}, Payload: &node}, nil
}

func (c importCandidate) read(scope, id ir.ID, index int) (imports.Candidate, error) {
	read := imports.Candidate{CandidateID: id, Index: index, Name: c.Name, State: c.State, Diagnostics: c.Diagnostics, ExistingResourceID: c.ExistingID, ExistingRevision: apicontract.Revision(c.ExistingRevision), MatchMethod: c.MatchMethod}
	if read.Diagnostics == nil {
		read.Diagnostics = ir.Diagnostics{}
	}
	if c.State != "invalid" {
		r, err := c.resource(scope, id)
		if err != nil {
			return imports.Candidate{}, err
		}
		response, err := apicontract.NewNodeReadResponse("import-preview", r)
		if err != nil {
			return imports.Candidate{}, imports.ErrUnavailable
		}
		read.Node = &response.Data.Node
	}
	return read, nil
}

func importError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{imports.ErrInvalidInput, imports.ErrUnsupported, imports.ErrNotFound, imports.ErrRevisionConflict, imports.ErrIdempotencyConflict, imports.ErrStateConflict, imports.ErrExpired, imports.ErrUnavailable, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	switch catalogError(err) {
	case catalog.ErrNotFound:
		return imports.ErrNotFound
	case catalog.ErrInvalidInput, catalog.ErrInvalidReference:
		return imports.ErrInvalidInput
	case catalog.ErrRevisionConflict:
		return imports.ErrRevisionConflict
	case catalog.ErrIdempotencyConflict:
		return imports.ErrIdempotencyConflict
	}
	var boundary *apicontract.Error
	if errors.As(err, &boundary) {
		return imports.ErrInvalidInput
	}
	return imports.ErrUnavailable
}

func rollbackImport(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
