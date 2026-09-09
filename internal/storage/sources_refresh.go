package storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/origin"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/safefetch"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/Runarry/ProxyLoom/internal/source"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type storedSourceItem struct {
	ID              ir.ID
	ExternalKey     string
	Fingerprint     string
	Name            string
	SuggestedNodeID ir.ID
	Node            *ir.Node
	State           string
	Revision        int64
}

type parsedCandidate struct {
	Name        string
	ExternalKey string
	Fingerprint string
	Node        ir.Node
	Diagnostics ir.Diagnostics
}

const sourcePreviewLimit = "SOURCE_PREVIEW_LIMIT"
const sourceInvalidEntries = "SOURCE_INVALID_ENTRIES"

func (s *Sources) HandleRefresh(ctx context.Context, lease jobs.Lease) (jobs.Result, jobs.CommitFunc, error) {
	if lease.Job.Executor != jobs.APIWorker || lease.Job.Type != jobs.SourceRefresh || !lease.Identity.Valid() || lease.Identity.JobID != lease.Job.ID || !validIDs(lease.Job.ScopeID, lease.Job.BatchID) {
		return jobs.Result{}, nil, jobs.ErrInvalidInput
	}
	var payload refreshPayload
	if json.Unmarshal(lease.Payload, &payload) != nil || payload.SourceID != lease.Job.BatchID || payload.Revision < 1 {
		return jobs.Result{}, nil, jobs.ErrInvalidInput
	}
	document, err := s.catalog.SourceHead(ctx, lease.Job.ScopeID, payload.SourceID)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return jobs.Result{State: jobs.Failed, Verdict: jobs.Fail, Error: runnerprotocol.Safe("INVALID_CONFIG")}, nil, nil
		}
		return jobs.Result{}, nil, err
	}
	if document.Metadata.Revision != payload.Revision {
		return jobs.Result{State: jobs.Failed, Verdict: jobs.Fail, Error: runnerprotocol.Safe("INVALID_CONFIG")}, nil, nil
	}
	fetched, fetchErr := s.fetchSource(ctx, document)
	now := time.Now().UTC()
	if fetchErr != nil || fetched.Status < 200 || fetched.Status > 299 || len(fetched.Body) == 0 {
		code := refreshFetchCode(fetchErr, fetched)
		return s.failedRefresh(document, lease.Job.ID, payload, fetched, code, now)
	}
	parsed, parseErr := importparse.Parse(ctx, source.ParserFormat(document.Source.Format), fetched.Body)
	if ctx.Err() != nil {
		return jobs.Result{}, nil, ctx.Err()
	}
	candidates := make([]parsedCandidate, 0, len(parsed.Candidates))
	if parseErr == nil {
		for _, item := range parsed.Candidates {
			if !item.Valid() {
				return s.failedRefresh(document, lease.Job.ID, payload, fetched, sourceInvalidEntries, now)
			}
			fingerprint, err := s.connectionFingerprint(*item.Node)
			if err != nil {
				return jobs.Result{}, nil, err
			}
			name := item.Name
			if name == "" {
				name = "Imported source node"
			}
			if (ir.Metadata{ResourceID: payload.SourceID, ScopeID: lease.Job.ScopeID, Kind: ir.KindNode, Revision: 1,
				SchemaVersion: 1, SecurityEpoch: 1, Name: name, Enabled: true, Tags: []string{}}).Validate() != nil {
				return s.failedRefresh(document, lease.Job.ID, payload, fetched, sourceInvalidEntries, now)
			}
			candidates = append(candidates, parsedCandidate{Name: name, ExternalKey: origin.ExternalKeyFromMetadata(item.Metadata),
				Fingerprint: fingerprint, Node: *item.Node, Diagnostics: item.Diagnostics})
		}
	}
	if parseErr != nil || len(candidates) == 0 {
		return s.failedRefresh(document, lease.Job.ID, payload, fetched, "INVALID_CONFIG", now)
	}
	existing, err := s.loadItems(ctx, &catalogTx{q: s.catalog.q, scope: lease.Job.ScopeID}, payload.SourceID)
	if err != nil {
		return jobs.Result{}, nil, err
	}
	if sourcePreviewCount(existing, candidates) > imports.MaxCandidates {
		return s.failedRefresh(document, lease.Job.ID, payload, fetched, sourcePreviewLimit, now)
	}
	return jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass}, func(ctx context.Context, tx pgx.Tx) error {
		return s.commitRefresh(ctx, tx, lease.Job, payload, document, fetched, candidates, now, true, "")
	}, nil
}

func (s *Sources) failedRefresh(document source.Document, job ir.ID, payload refreshPayload, fetched safefetch.Result, code string, now time.Time) (jobs.Result, jobs.CommitFunc, error) {
	return jobs.Result{State: jobs.Failed, Verdict: jobs.Fail, Error: sourceRefreshError(code)}, func(ctx context.Context, tx pgx.Tx) error {
		return s.commitRefresh(ctx, tx, jobs.Job{ID: job, ScopeID: document.Metadata.ScopeID, BatchID: document.Metadata.ResourceID}, payload, document, fetched, nil, now, false, code)
	}, nil
}

func sourceRefreshError(code string) *runnerprotocol.SafeError {
	switch code {
	case sourcePreviewLimit:
		err := runnerprotocol.Safe("VALIDATION_FAILED")
		err.Message = "The source preview exceeds the 5000-candidate limit, including missing items. No changes were applied."
		return err
	case sourceInvalidEntries:
		err := runnerprotocol.Safe("VALIDATION_FAILED")
		err.Message = "The source contains invalid or unsupported entries. Previous successful data was retained."
		return err
	default:
		return runnerprotocol.Safe(code)
	}
}

func refreshFetchCode(err error, fetched safefetch.Result) string {
	switch {
	case errors.Is(err, safefetch.ErrTimeout):
		return "JOB_TIMEOUT"
	case errors.Is(err, safefetch.ErrInvalidURL), errors.Is(err, safefetch.ErrHTTPDisabled), errors.Is(err, safefetch.ErrBlockedAddress), errors.Is(err, safefetch.ErrTooLarge):
		return "INVALID_CONFIG"
	case err == nil && fetched.Status >= 200 && fetched.Status <= 299 && len(fetched.Body) == 0:
		return "INVALID_CONFIG"
	case err != nil, fetched.Status < 200, fetched.Status > 299, len(fetched.Body) == 0:
		return "SERVICE_UNAVAILABLE"
	default:
		return "SERVICE_UNAVAILABLE"
	}
}

func (s *Sources) fetchSource(ctx context.Context, document source.Document) (safefetch.Result, error) {
	header := http.Header{}
	switch document.Source.Auth.Kind {
	case source.AuthBearer:
		header.Set("Authorization", "Bearer "+string(document.Source.Auth.Token))
	case source.AuthBasic:
		header.Set("Authorization", "Basic "+basicAuth(string(document.Source.Auth.Username), string(document.Source.Auth.Password)))
	}
	parsed, err := source.ParseURL(string(document.Source.URL))
	if err != nil {
		return safefetch.Result{}, safefetch.ErrInvalidURL
	}
	return s.fetch.Fetch(ctx, string(document.Source.URL), safefetch.Request{Header: header, AllowHTTP: parsed.Scheme == "http",
		Timeout:      time.Duration(document.Source.FetchLimits.TimeoutMS) * time.Millisecond,
		MaxRedirects: document.Source.FetchLimits.MaxRedirects, MaxCompressedBytes: int64(document.Source.FetchLimits.MaxCompressedBytes),
		MaxDecodedBytes: int64(document.Source.FetchLimits.MaxDecodedBytes)})
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

func (s *Sources) commitRefresh(ctx context.Context, tx pgx.Tx, job jobs.Job, payload refreshPayload, observed source.Document, fetched safefetch.Result, candidates []parsedCandidate, now time.Time, success bool, code string) error {
	t := attachCatalogTx(s.catalog, tx, job.ScopeID)
	defer t.close()
	if _, err := t.q.LockScope(ctx, dbID(job.ScopeID)); err != nil {
		return err
	}
	if err := t.lockAndCheckRefs(ctx, []ir.ID{payload.SourceID}, nil); err != nil {
		return err
	}
	current, err := t.sourceHead(ctx, payload.SourceID, true)
	if err != nil {
		return err
	}
	if current.Metadata.Revision != payload.Revision {
		return nil
	}
	snapshotID, err := s.insertSnapshot(ctx, t, current, fetched, success)
	if err != nil {
		return err
	}
	cfg := current.Source
	cfg.LastJobID = job.ID
	if success {
		cfg.LastError = nil
		cfg.LastSuccessAt = &now
		cfg.BindingRevision++
		batchID, err := s.createSourcePreview(ctx, t, current, payload, job.ID, snapshotID, candidates, now)
		if err != nil {
			return err
		}
		cfg.LatestPreviewBatchID = batchID
	} else {
		cfg.LastError = sourceRefreshError(code)
	}
	next, err := t.UpdateSource(ctx, current.Metadata.ResourceID, current.Metadata.Revision, current.Metadata.Name, current.Metadata.Tags, current.Metadata.Enabled, cfg)
	if err != nil {
		return err
	}
	ok := success
	if err := s.syncSchedule(ctx, t, next, false, job.ID, &ok, now); err != nil {
		return err
	}
	if t.dirty {
		return t.q.AdvanceCatalog(ctx, dbID(job.ScopeID))
	}
	return t.failed
}

func (s *Sources) insertSnapshot(ctx context.Context, t *catalogTx, document source.Document, fetched safefetch.Result, success bool) (ir.ID, error) {
	id, err := newResourceID()
	if err != nil {
		return "", err
	}
	body := fetched.Body
	if body == nil {
		body = []byte{}
	}
	envelope, wrapping, digest, err := s.sealRecord(document.Metadata.ScopeID, secretbox.TableSourceSnapshots, id, 1, body)
	if err != nil {
		return "", err
	}
	state := "failed"
	if success {
		state = "success"
	}
	var status pgtype.Int4
	if fetched.Status >= 100 && fetched.Status <= 599 {
		status = pgtype.Int4{Int32: int32(fetched.Status), Valid: true}
	}
	var contentType pgtype.Text
	if fetched.ContentType != "" {
		contentType = pgtype.Text{String: fetched.ContentType, Valid: true}
	}
	err = t.q.InsertSourceSnapshot(ctx, dbgen.InsertSourceSnapshotParams{ID: dbID(id), ScopeID: dbID(document.Metadata.ScopeID),
		SourceID: dbID(document.Metadata.ResourceID), SourceRevision: document.Metadata.Revision, Envelope: envelope, Wrapping: wrapping,
		ContentHmac: digest, HttpStatus: status, ContentType: contentType, DecodedBytes: int32(len(body)), State: state})
	return id, err
}

func sameItem(item storedSourceItem, candidate parsedCandidate) bool {
	if item.ExternalKey != "" && candidate.ExternalKey != "" {
		return item.ExternalKey == candidate.ExternalKey
	}
	return item.Fingerprint != "" && item.Fingerprint == candidate.Fingerprint
}

func matchStoredItem(items []storedSourceItem, candidate parsedCandidate) (storedSourceItem, bool) {
	for _, item := range items {
		if sameItem(item, candidate) {
			return item, true
		}
	}
	return storedSourceItem{}, false
}

func (s *Sources) loadItems(ctx context.Context, t *catalogTx, sourceID ir.ID) ([]storedSourceItem, error) {
	rows, err := t.q.ListSourceItems(ctx, dbgen.ListSourceItemsParams{ScopeID: dbID(t.scope), SourceID: dbID(sourceID)})
	if err != nil {
		return nil, err
	}
	items := make([]storedSourceItem, 0, len(rows))
	for _, row := range rows {
		id := irID(row.ID)
		plain, err := s.openRecord(t.scope, secretbox.TableSourceItems, id, row.BaseRevision, row.Envelope, row.Wrapping, nil)
		if err != nil {
			return nil, err
		}
		var stored struct {
			Name, ExternalKey, Fingerprint string
			SuggestedNodeID                ir.ID `json:"suggested_node_id"`
			Node                           *ir.Node
		}
		if json.Unmarshal(plain, &stored) != nil {
			clear(plain)
			return nil, catalog.ErrCrypto
		}
		clear(plain)
		item := storedSourceItem{ID: id, ExternalKey: stored.ExternalKey, Fingerprint: stored.Fingerprint, Name: stored.Name, SuggestedNodeID: stored.SuggestedNodeID, Node: stored.Node, State: row.State, Revision: row.BaseRevision}
		if row.ExternalKey.Valid {
			item.ExternalKey = row.ExternalKey.String
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Sources) itemBytes(candidate parsedCandidate) ([]byte, error) {
	return s.itemRecordBytes(candidate.Name, candidate.ExternalKey, candidate.Fingerprint, "", candidate.Node)
}

func (s *Sources) itemRecordBytes(name, externalKey, fingerprint string, suggested ir.ID, node ir.Node) ([]byte, error) {
	return json.Marshal(struct {
		Name, ExternalKey, Fingerprint string
		SuggestedNodeID                ir.ID `json:"suggested_node_id,omitempty"`
		Node                           ir.Node
	}{name, externalKey, fingerprint, suggested, node})
}

func (s *Sources) insertItem(ctx context.Context, t *catalogTx, document source.Document, id ir.ID, candidate parsedCandidate, now time.Time) error {
	plain, err := s.itemBytes(candidate)
	if err != nil {
		return catalog.ErrUnavailable
	}
	defer clear(plain)
	envelope, wrapping, _, err := s.sealRecord(t.scope, secretbox.TableSourceItems, id, document.Metadata.Revision, plain)
	if err != nil {
		return err
	}
	var key pgtype.Text
	if candidate.ExternalKey != "" {
		key = pgtype.Text{String: candidate.ExternalKey, Valid: true}
	}
	return t.q.InsertSourceItem(ctx, dbgen.InsertSourceItemParams{ID: dbID(id), ScopeID: dbID(t.scope), SourceID: dbID(document.Metadata.ResourceID),
		ExternalKey: key, Envelope: envelope, Wrapping: wrapping, BaseRevision: document.Metadata.Revision,
		LastSeenAt: pgtype.Timestamptz{Time: now, Valid: true}, State: "active"})
}

func (s *Sources) updateItem(ctx context.Context, t *catalogTx, document source.Document, id ir.ID, candidate parsedCandidate, now time.Time) error {
	// A pending manual preview must not replace the baseline used by overlays.
	if _, err := t.tx.Exec(ctx, `UPDATE public.source_items SET applied_envelope=envelope,applied_wrapping=wrapping,applied_revision=base_revision
		WHERE scope_id=$1 AND id=$2 AND applied_revision IS NULL
		AND EXISTS(SELECT 1 FROM public.node_bindings b WHERE b.scope_id=$1 AND b.source_item_id=$2)`, dbID(t.scope), dbID(id)); err != nil {
		return err
	}
	plain, err := s.itemBytes(candidate)
	if err != nil {
		return catalog.ErrUnavailable
	}
	defer clear(plain)
	envelope, wrapping, _, err := s.sealRecord(t.scope, secretbox.TableSourceItems, id, document.Metadata.Revision, plain)
	if err != nil {
		return err
	}
	return t.q.UpdateSourceItem(ctx, dbgen.UpdateSourceItemParams{ScopeID: dbID(t.scope), ID: dbID(id), Envelope: envelope, Wrapping: wrapping,
		BaseRevision: document.Metadata.Revision, LastSeenAt: pgtype.Timestamptz{Time: now, Valid: true}, State: "active"})
}

func (s *Sources) markMissing(ctx context.Context, t *catalogTx, document source.Document, payload refreshPayload, item storedSourceItem, now time.Time) error {
	candidate := parsedCandidate{Name: item.Name, ExternalKey: item.ExternalKey, Fingerprint: item.Fingerprint}
	if item.Node != nil {
		candidate.Node = *item.Node
	}
	plain, err := s.itemBytes(candidate)
	if err != nil {
		return catalog.ErrUnavailable
	}
	defer clear(plain)
	envelope, wrapping, _, err := s.sealRecord(t.scope, secretbox.TableSourceItems, item.ID, item.Revision, plain)
	if err != nil {
		return err
	}
	if err := t.q.UpdateSourceItem(ctx, dbgen.UpdateSourceItemParams{ScopeID: dbID(t.scope), ID: dbID(item.ID), Envelope: envelope, Wrapping: wrapping,
		BaseRevision: item.Revision, LastSeenAt: pgtype.Timestamptz{Time: now, Valid: true}, State: "missing"}); err != nil {
		return err
	}
	binding, err := t.loadBindingByItem(ctx, item.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if binding.State != override.Stale {
		binding.State = override.Stale
		binding.Revision++
		if err := t.saveBinding(ctx, binding); err != nil {
			return err
		}
	}
	if document.Source.RefreshPolicy.MissingPolicy != source.Disable || document.Source.RefreshPolicy.CommitMode != source.SafeUpdates {
		return nil
	}
	resource, err := t.Probe(ctx, binding.NodeID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !resource.Metadata.Enabled {
		return nil
	}
	updated, err := t.Update(ctx, resource.Metadata.ResourceID, resource.Metadata.Revision, catalog.UpdateInput{
		Name: resource.Metadata.Name, Tags: resource.Metadata.Tags, Enabled: false, Payload: resource.Payload})
	if err != nil {
		return err
	}
	if payload.ActorID.Validate() == nil && payload.RequestID != "" {
		return t.Audit(ctx, catalog.MutationAudit{PrincipalID: payload.ActorID, ObjectID: updated.Metadata.ResourceID,
			Revision: updated.Metadata.Revision, RequestID: payload.RequestID, Action: catalog.AuditNodeSetEnabled})
	}
	return nil
}

func (s *Sources) commitNode(ctx context.Context, t *catalogTx, document source.Document, payload refreshPayload, candidate parsedCandidate, item storedSourceItem, records []origin.Record, now time.Time) error {
	if document.Source.RefreshPolicy.CommitMode != source.SafeUpdates || candidate.Node.Validate() != nil {
		return nil
	}
	decision, ok := origin.Match(origin.Item{SourceItemID: item.ID, ExternalKey: candidate.ExternalKey, Fingerprint: candidate.Fingerprint,
		Name: candidate.Name, Protocol: candidate.Node.Protocol, Host: candidate.Node.Endpoint.Host, Port: candidate.Node.Endpoint.Port}, records)
	if ok && (decision.Kind == origin.Duplicate || decision.Kind == origin.Suggest || decision.Ambiguous) {
		return s.markConflict(ctx, t, document, item, decision, now)
	}
	if ok && decision.Kind == origin.Identity {
		return s.updateBoundNode(ctx, t, document, payload, candidate, item, decision)
	}
	return s.createBoundNode(ctx, t, document, payload, candidate, item)
}

func (s *Sources) markConflict(ctx context.Context, t *catalogTx, document source.Document, item storedSourceItem, decision origin.Decision, now time.Time) error {
	plain, err := s.itemRecordBytes(item.Name, item.ExternalKey, item.Fingerprint, decision.NodeID, derefNode(item.Node))
	if err != nil {
		return catalog.ErrUnavailable
	}
	defer clear(plain)
	envelope, wrapping, _, err := s.sealRecord(t.scope, secretbox.TableSourceItems, item.ID, document.Metadata.Revision, plain)
	if err != nil {
		return err
	}
	if err := t.q.UpdateSourceItem(ctx, dbgen.UpdateSourceItemParams{ScopeID: dbID(t.scope), ID: dbID(item.ID), Envelope: envelope, Wrapping: wrapping,
		BaseRevision: document.Metadata.Revision, LastSeenAt: pgtype.Timestamptz{Time: now, Valid: true}, State: "conflict"}); err != nil {
		return err
	}
	binding, err := t.loadBindingByItem(ctx, item.ID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	binding.State = override.Conflict
	binding.Revision++
	return t.saveBinding(ctx, binding)
}

func (s *Sources) updateBoundNode(ctx context.Context, t *catalogTx, document source.Document, payload refreshPayload, candidate parsedCandidate, item storedSourceItem, decision origin.Decision) error {
	resource, err := t.Head(ctx, decision.NodeID)
	if err != nil {
		return err
	}
	node, ok := resource.Payload.(*ir.Node)
	if !ok {
		return catalog.ErrInvalidInput
	}
	binding, bindErr := t.loadBindingByNode(ctx, resource.Metadata.ResourceID)
	patch := override.Document{}
	if bindErr == nil {
		patch = binding.Patch
	} else if !errors.Is(bindErr, catalog.ErrNotFound) {
		return bindErr
	}
	baseName := candidate.Name
	if patch.Name != nil {
		baseName = resource.Metadata.Name
	}
	next, name, _, err := override.Merge(candidate.Node, baseName, patch)
	if err != nil {
		return catalog.ErrInvalidInput
	}
	next.Origin = node.Origin
	if next.Origin == nil {
		next.Origin = &ir.Origin{SourceResourceID: document.Metadata.ResourceID, SourceItemID: item.ID, MatchMethod: ir.MatchMethod(decision.Method)}
	} else {
		next.Origin.MatchMethod = ir.MatchMethod(decision.Method)
		next.Origin.SourceItemID = item.ID
		next.Origin.SourceResourceID = document.Metadata.ResourceID
	}
	updated, err := t.Update(ctx, resource.Metadata.ResourceID, resource.Metadata.Revision, catalog.UpdateInput{
		Name: name, Tags: resource.Metadata.Tags, Enabled: resource.Metadata.Enabled, Payload: &next})
	if err != nil {
		return err
	}
	if bindErr != nil {
		binding = override.Binding{NodeID: updated.Metadata.ResourceID, SourceItemID: item.ID, SourceResourceID: document.Metadata.ResourceID, Revision: 0}
	}
	binding.SourceItemID = item.ID
	binding.SourceResourceID = document.Metadata.ResourceID
	binding.Method = ir.MatchMethod(decision.Method)
	binding.State = override.Active
	binding.Patch = patch
	binding.Revision++
	if err := t.saveBinding(ctx, binding); err != nil {
		return err
	}
	if payload.ActorID.Validate() == nil && payload.RequestID != "" {
		return t.Audit(ctx, catalog.MutationAudit{PrincipalID: payload.ActorID, ObjectID: updated.Metadata.ResourceID,
			Revision: updated.Metadata.Revision, RequestID: payload.RequestID, Action: catalog.AuditNodeUpdate})
	}
	return nil
}

func (s *Sources) createBoundNode(ctx context.Context, t *catalogTx, document source.Document, payload refreshPayload, candidate parsedCandidate, item storedSourceItem) error {
	method := ir.ExactFingerprint
	if candidate.ExternalKey != "" {
		method = ir.StableExternalKey
	}
	node := candidate.Node
	node.Origin = &ir.Origin{SourceResourceID: document.Metadata.ResourceID, SourceItemID: item.ID, MatchMethod: method}
	created, err := t.Create(ctx, catalog.CreateInput{Name: candidate.Name, Tags: []string{}, Enabled: true, Payload: &node})
	if err != nil {
		return err
	}
	if err := t.saveBinding(ctx, override.Binding{
		NodeID: created.Metadata.ResourceID, SourceItemID: item.ID, SourceResourceID: document.Metadata.ResourceID,
		Revision: 1, Method: method, State: override.Active,
	}); err != nil {
		return err
	}
	if payload.ActorID.Validate() == nil && payload.RequestID != "" {
		return t.Audit(ctx, catalog.MutationAudit{PrincipalID: payload.ActorID, ObjectID: created.Metadata.ResourceID,
			Revision: created.Metadata.Revision, RequestID: payload.RequestID, Action: catalog.AuditNodeCreate})
	}
	return nil
}

func derefNode(node *ir.Node) ir.Node {
	if node == nil {
		return ir.Node{}
	}
	return *node
}

func (s *Sources) nodeRecords(ctx context.Context, t *catalogTx, sourceID ir.ID, items []storedSourceItem) ([]origin.Record, error) {
	_ = sourceID
	itemByID := map[ir.ID]storedSourceItem{}
	for _, item := range items {
		itemByID[item.ID] = item
	}
	options := catalog.NodeListOptions{Limit: 200}
	records := []origin.Record{}
	for {
		page, err := t.store.ListNodes(ctx, t.scope, options)
		if err != nil {
			return nil, err
		}
		for _, resource := range page.Items {
			node, ok := resource.Payload.(*ir.Node)
			if !ok {
				return nil, catalog.ErrCrypto
			}
			fingerprint, err := s.connectionFingerprint(*node)
			if err != nil {
				return nil, err
			}
			record := origin.Record{NodeID: resource.Metadata.ResourceID, Revision: resource.Metadata.Revision, Fingerprint: fingerprint,
				Name: resource.Metadata.Name, Protocol: node.Protocol, Host: node.Endpoint.Host, Port: node.Endpoint.Port}
			binding, err := t.q.GetNodeBindingByNode(ctx, dbgen.GetNodeBindingByNodeParams{ScopeID: dbID(t.scope), NodeID: dbID(resource.Metadata.ResourceID)})
			if err == nil {
				itemID := irID(binding.SourceItemID)
				if item, ok := itemByID[itemID]; ok {
					record.BoundSourceItemID = itemID
					record.ExternalKey = item.ExternalKey
				}
			} else if !errors.Is(catalogError(err), catalog.ErrNotFound) {
				return nil, err
			}
			records = append(records, record)
		}
		if page.Next == nil {
			return records, nil
		}
		options.After = page.Next
	}
}

func (s *Sources) connectionFingerprint(node ir.Node) (string, error) {
	canonical, err := importparse.CanonicalConnection(node)
	if err != nil {
		return "", catalog.ErrInvalidInput
	}
	defer clear(canonical)
	digest, err := s.catalog.box.Digest(secretbox.PurposeImportConnection, canonical)
	if err != nil {
		return "", catalog.ErrUnavailable
	}
	return hex.EncodeToString(digest), nil
}

func cloneNode(node ir.Node) *ir.Node {
	copy := node
	return &copy
}
