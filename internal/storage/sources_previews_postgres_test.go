package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/source"
)

type sourcePreviewFixture struct {
	h               *sourceHTTPAcceptance
	imports         *Imports
	actor, sourceID ir.ID
	body            atomic.Value
	hits            atomic.Int32
}

func newSourcePreviewFixture(t *testing.T, mode string) *sourcePreviewFixture {
	t.Helper()
	h := newSourceHTTPAcceptance(t)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(h.env.ctx), 3*time.Minute)
	t.Cleanup(cancel)
	h.env.ctx = ctx
	store, err := NewImports(h.env.store, h.queue)
	if err != nil {
		t.Fatal(err)
	}
	f := &sourcePreviewFixture{h: h, imports: store}
	if err := h.env.runtime.QueryRow(ctx, `SELECT id FROM public.users WHERE scope_id=$1`, dbID(h.env.scope)).Scan(&f.actor); err != nil {
		t.Fatal("source actor unavailable")
	}
	f.body.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.hits.Add(1)
		_, _ = io.WriteString(w, f.body.Load().(string))
	}))
	t.Cleanup(upstream.Close)
	created := requireSource(t, h.do(http.MethodPost, "/api/v1/sources", sourceBody(t, "Preview source", upstream.URL, mode, true), "", nil), http.StatusCreated)
	f.sourceID = created.Metadata.ResourceID
	return f
}

func sourcePreviewURI(key, name, host, password string) string {
	uri := url.URL{Scheme: "trojan", Host: host + ":443", User: url.User(password), Fragment: name}
	query := url.Values{"security": {"tls"}, "sni": {host}}
	if key != "" {
		query.Set("external_key", key)
	}
	uri.RawQuery = query.Encode()
	return uri.String() + "\n"
}

func (f *sourcePreviewFixture) refresh(t *testing.T, body string) imports.Batch {
	t.Helper()
	f.body.Store(body)
	doc, err := f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	refreshSource(t, f.h, f.sourceID, doc.Metadata.Revision)
	doc, err = f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source.LatestPreviewBatchID == "" {
		t.Fatal("successful refresh did not expose its preview")
	}
	batch, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, doc.Source.LatestPreviewBatchID, imports.PageOptions{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func (f *sourcePreviewFixture) input(batch imports.Batch, decisions ...imports.Decision) imports.CommitInput {
	return imports.CommitInput{ScopeID: f.h.env.scope, PrincipalID: f.actor, BatchID: batch.BatchID,
		ExpectedRevision: int64(batch.Revision), SourceRevision: int64(batch.SourceRevision), RequestID: "source-preview-acceptance", Decisions: decisions}
}

func previewDecision(candidate imports.Candidate, action string) imports.Decision {
	decision := imports.Decision{CandidateID: candidate.CandidateID, Action: action}
	if action == "update" || action == "bind" {
		decision.ResourceID, decision.ExpectedRevision, decision.ExpectedBindingRevision = candidate.ExistingResourceID, candidate.ExistingRevision, candidate.BindingRevision
	}
	return decision
}

func previewCandidate(t *testing.T, batch imports.Batch, name string) imports.Candidate {
	t.Helper()
	for _, candidate := range batch.Candidates {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatal("named preview candidate missing")
	return imports.Candidate{}
}

func sourceNodeCount(t *testing.T, f *sourcePreviewFixture) int {
	t.Helper()
	return importCount(t, f.h.env, `SELECT count(*) FROM public.resources WHERE kind='node' AND deleted_at IS NULL`)
}

func TestPostgresSourcePreviewManualCrossPageFailureAndReplay(t *testing.T) {
	f := newSourcePreviewFixture(t, "manual")
	body := sourcePreviewURI("a", "A", "a.example.invalid", "SYNTHETIC_PREVIEW_A") + sourcePreviewURI("b", "B", "b.example.invalid", "SYNTHETIC_PREVIEW_B")
	batch := f.refresh(t, body)
	if batch.State != "ready" || batch.SourceID != f.sourceID || batch.SnapshotID.Validate() != nil || batch.SourceRevision == 0 || batch.CandidateCount != 2 {
		t.Fatal("source batch association incomplete")
	}
	if sourceNodeCount(t, f) != 0 {
		t.Fatal("manual preview created an effective node")
	}
	if batch.ExpiresAt.Sub(batch.CreatedAt) != imports.Retention {
		t.Fatal("source preview retention differs from imports")
	}
	page, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, batch.BatchID, imports.PageOptions{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatal("source preview first page missing cursor")
	}
	last, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, batch.BatchID, imports.PageOptions{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(last.Candidates) != 1 || last.NextCursor != "" {
		t.Fatal("source preview second page failed")
	}
	for _, candidate := range batch.Candidates {
		if candidate.ChangeKind != "new" || candidate.AutoApplied || candidate.SourceItemID == "" {
			t.Fatal("manual new candidate incorrect")
		}
	}
	if count := importCount(t, f.h.env, `SELECT count(*) FROM public.import_batches WHERE source_id=$1 AND raw_envelope IS NOT NULL`, dbID(f.sourceID)); count != 0 {
		t.Fatal("source batch duplicated raw subscription input")
	}
	encoded, err := json.Marshal(batch)
	if err != nil || strings.Contains(string(encoded), "SYNTHETIC_PREVIEW_") {
		t.Fatal("source preview disclosed a synthetic secret")
	}
	// Empty and mixed-invalid refreshes preserve the successful preview and
	// its source revision, and cannot silently mark successful items missing.
	failed := f.refresh(t, "")
	if failed.BatchID != batch.BatchID || failed.State != "ready" {
		t.Fatal("empty refresh replaced successful pending data")
	}
	failed = f.refresh(t, body+"unsupported://invalid\n")
	if failed.BatchID != batch.BatchID || sourceNodeCount(t, f) != 0 {
		t.Fatal("invalid refresh changed successful pending data")
	}
	input := f.input(batch, previewDecision(page.Candidates[0], "create"), previewDecision(last.Candidates[0], "create"))
	input.IdempotencyKey = "source-preview-create"
	hits := f.hits.Load()
	receipt, err := f.imports.Commit(f.h.env.ctx, input)
	if err != nil || len(receipt.Items) != 2 {
		t.Fatalf("manual source confirmation failed: %v", err)
	}
	if f.hits.Load() != hits {
		t.Fatal("source confirmation fetched upstream again")
	}
	again, err := f.imports.Commit(f.h.env.ctx, input)
	if err != nil || !again.Replayed || again.Items[0].ResourceID != receipt.Items[0].ResourceID || sourceNodeCount(t, f) != 2 {
		t.Fatal("source commit replay was not idempotent")
	}
	for _, item := range receipt.Items {
		binding, err := f.h.env.store.NodeBinding(f.h.env.ctx, f.h.env.scope, item.ResourceID)
		if err != nil || binding.SourceResourceID != f.sourceID || binding.Revision != 1 {
			t.Fatal("confirmation did not atomically bind its node")
		}
	}
}

func TestPostgresSourcePreviewPendingOverlayAndAtomicRevisionConflicts(t *testing.T) {
	f := newSourcePreviewFixture(t, "manual")
	initial := sourcePreviewURI("a", "A", "old.example.invalid", "SYNTHETIC_PREVIEW_OLD")
	first := f.refresh(t, initial)
	created, err := f.imports.Commit(f.h.env.ctx, f.input(first, previewDecision(first.Candidates[0], "create")))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Items[0].ResourceID
	localPatch, err := json.Marshal(map[string]any{"binding_revision": "1", "node": map[string]any{"auth": map[string]any{
		"kind": "password", "password": ir.Secret("SYNTHETIC_LOCAL_OVERRIDE"),
	}}})
	if err != nil {
		t.Fatal("local override fixture unavailable")
	}
	local := requireNode(t, f.h.do(http.MethodPatch, "/api/v1/nodes/"+string(id),
		string(localPatch), revisionTag(1), nil), http.StatusOK)
	body := sourcePreviewURI("a", "A", "new.example.invalid", "SYNTHETIC_PREVIEW_NEW") + sourcePreviewURI("b", "B", "b.example.invalid", "SYNTHETIC_PREVIEW_B")
	pending := f.refresh(t, body)
	changed := previewCandidate(t, pending, "A")
	newCandidate := previewCandidate(t, pending, "B")
	if changed.ChangeKind != "modified" || changed.AutoApplied || changed.ExistingRevision != local.Metadata.Revision {
		t.Fatal("manual change was applied before confirmation")
	}
	secretUpstream, secretEffective := false, false
	for _, change := range changed.UpstreamChanges {
		secretUpstream = secretUpstream || change.FieldPath == "/node/auth/password" && change.SecretChanged
	}
	for _, change := range changed.EffectiveChanges {
		secretEffective = secretEffective || change.FieldPath == "/node/auth/password"
	}
	if !secretUpstream || secretEffective {
		t.Fatal("source diff did not separate overridden secret changes")
	}
	patched := requireNode(t, f.h.do(http.MethodPatch, "/api/v1/nodes/"+string(id),
		`{"name":"Local pending edit","binding_revision":"`+revisionDecimal(int64(local.Binding.BindingRevision))+`"}`, revisionTag(int64(local.Metadata.Revision)), nil), http.StatusOK)
	if patched.Node.Endpoint.Host != "old.example.invalid" {
		t.Fatal("local overlay pulled in unconfirmed upstream fields")
	}
	resource, err := f.h.env.store.Head(f.h.env.ctx, f.h.env.scope, id)
	if err != nil || resource.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != "SYNTHETIC_LOCAL_OVERRIDE" {
		t.Fatal("pending local overlay lost the confirmed secret")
	}
	_, err = f.imports.Commit(f.h.env.ctx, f.input(pending, previewDecision(newCandidate, "create"), previewDecision(changed, "update")))
	if !errors.Is(err, imports.ErrRevisionConflict) || sourceNodeCount(t, f) != 1 {
		t.Fatal("node revision conflict did not roll back all selected changes")
	}
	fresh := f.refresh(t, body)
	changed, newCandidate = previewCandidate(t, fresh, "A"), previewCandidate(t, fresh, "B")
	if err := f.h.env.store.transactRaw(f.h.env.ctx, f.h.env.scope, func(tx *catalogTx) error {
		binding, err := tx.loadBindingByNode(f.h.env.ctx, id)
		if err != nil {
			return err
		}
		binding.Revision++
		return tx.saveBinding(f.h.env.ctx, binding)
	}); err != nil {
		t.Fatal(err)
	}
	_, err = f.imports.Commit(f.h.env.ctx, f.input(fresh, previewDecision(newCandidate, "create"), previewDecision(changed, "update")))
	if !errors.Is(err, imports.ErrStateConflict) || sourceNodeCount(t, f) != 1 {
		t.Fatal("binding-only conflict did not return state conflict and roll back")
	}
	fresh = f.refresh(t, body)
	changed, newCandidate = previewCandidate(t, fresh, "A"), previewCandidate(t, fresh, "B")
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(fresh, previewDecision(newCandidate, "create"), previewDecision(changed, "update"))); err != nil {
		t.Fatal(err)
	}
	resource, err = f.h.env.store.Head(f.h.env.ctx, f.h.env.scope, id)
	if err != nil || resource.Payload.(*ir.Node).Endpoint.Host != "new.example.invalid" || resource.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != "SYNTHETIC_LOCAL_OVERRIDE" {
		t.Fatal("confirmed upstream did not preserve local overrides")
	}
}

func TestPostgresSourcePreviewSuggestionBindingOwnershipAndMissingSkip(t *testing.T) {
	f := newSourcePreviewFixture(t, "manual")
	localURI := sourcePreviewURI("", "Suggestion", "suggest.example.invalid", "SYNTHETIC_SUGGEST_OLD")
	parsed := importparse.ParseURI(strings.TrimSpace(localURI))
	if !parsed.Valid() {
		t.Fatal("synthetic suggestion fixture invalid")
	}
	var local ir.Resource
	if err := f.h.env.store.transactRaw(f.h.env.ctx, f.h.env.scope, func(tx *catalogTx) error {
		var err error
		local, err = tx.Create(f.h.env.ctx, catalog.CreateInput{Name: "Suggestion", Tags: []string{}, Enabled: true, Payload: parsed.Node})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	body := sourcePreviewURI("a", "Suggestion", "suggest.example.invalid", "SYNTHETIC_SUGGEST_NEW")
	batch := f.refresh(t, body)
	if batch.Candidates[0].ChangeKind != "conflict" || batch.Candidates[0].BindingRevision != 0 || batch.Candidates[0].ExistingResourceID != local.Metadata.ResourceID {
		t.Fatal("unbound suggestion was not exposed")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(batch, previewDecision(batch.Candidates[0], "update"))); !errors.Is(err, imports.ErrInvalidInput) {
		t.Fatal("suggestion bypassed explicit bind via update")
	}
	bound, err := f.imports.Commit(f.h.env.ctx, f.input(batch, previewDecision(batch.Candidates[0], "bind")))
	if err != nil || bound.Items[0].ResourceID != local.Metadata.ResourceID || sourceNodeCount(t, f) != 1 {
		t.Fatalf("unbound suggestion confirmation failed: %v", err)
	}
	// A new item without a stable key can explicitly rebind the same source's
	// old node. The missing old item is included as a read-only skip selection.
	batch = f.refresh(t, sourcePreviewURI("b", "Suggestion", "suggest.example.invalid", "SYNTHETIC_SUGGEST_NEXT"))
	var decisions []imports.Decision
	var nextItem ir.ID
	for _, candidate := range batch.Candidates {
		action := "skip"
		if candidate.ChangeKind == "conflict" {
			action = "bind"
			nextItem = candidate.SourceItemID
		}
		decisions = append(decisions, previewDecision(candidate, action))
	}
	if nextItem == "" {
		t.Fatal("same-source rebind candidate missing")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(batch, decisions...)); err != nil {
		t.Fatalf("same-source rebind failed: %v", err)
	}
	binding, err := f.h.env.store.NodeBinding(f.h.env.ctx, f.h.env.scope, local.Metadata.ResourceID)
	if err != nil || binding.SourceItemID != nextItem {
		t.Fatal("same-source rebind did not persist its source item")
	}
	other := requireSource(t, f.h.do(http.MethodPost, "/api/v1/sources", sourceBody(t, "Other source", "https://other.example.invalid/feed", "manual", true), "", nil), http.StatusCreated)
	// The existing source's trusted fetch endpoint is reused by this fixture;
	// only source identity changes, which must never transfer its binding.
	current, err := f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	otherDoc, err := f.h.sources.Head(f.h.env.ctx, f.h.env.scope, other.Metadata.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	otherDoc.Source.URL = current.Source.URL
	if _, err := f.h.sources.Update(f.h.env.ctx, source.Mutation{ScopeID: f.h.env.scope, PrincipalID: f.actor, ResourceID: other.Metadata.ResourceID,
		ExpectedRevision: otherDoc.Metadata.Revision, Name: otherDoc.Metadata.Name, Tags: otherDoc.Metadata.Tags, Enabled: true, Config: otherDoc.Source, RequestID: "other-source-test"}); err != nil {
		t.Fatal(err)
	}
	f.sourceID = other.Metadata.ResourceID
	otherBatch := f.refresh(t, sourcePreviewURI("foreign", "Suggestion", "suggest.example.invalid", "SYNTHETIC_SUGGEST_NEXT")+sourcePreviewURI("new", "New", "new.example.invalid", "SYNTHETIC_OTHER_NEW"))
	foreign, added := previewCandidate(t, otherBatch, "Suggestion"), previewCandidate(t, otherBatch, "New")
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(otherBatch, previewDecision(added, "create"), previewDecision(foreign, "bind"))); !errors.Is(err, imports.ErrStateConflict) || sourceNodeCount(t, f) != 1 {
		t.Fatal("cross-source binding stole a node or partially committed")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(otherBatch, previewDecision(added, "skip"), previewDecision(foreign, "create"))); err != nil {
		t.Fatal("explicit separate creation failed")
	}
	kept, err := f.h.env.store.NodeBinding(f.h.env.ctx, f.h.env.scope, local.Metadata.ResourceID)
	if err != nil || kept.SourceResourceID != binding.SourceResourceID || kept.SourceItemID != binding.SourceItemID {
		t.Fatal("separate creation changed another source's binding")
	}
}

func TestPostgresSourcePreviewSafeSupersedeExpiryAndScheduledActor(t *testing.T) {
	f := newSourcePreviewFixture(t, "safe_updates")
	body := sourcePreviewURI("a", "A", "a.example.invalid", "SYNTHETIC_SAFE_A")
	first := f.refresh(t, body)
	if !first.Candidates[0].AutoApplied || sourceNodeCount(t, f) != 1 {
		t.Fatal("safe update did not expose its automatic application")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(first, previewDecision(first.Candidates[0], "update"))); !errors.Is(err, imports.ErrStateConflict) {
		t.Fatal("already applied candidate was writable again")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(first, previewDecision(first.Candidates[0], "skip"))); err != nil {
		t.Fatal("automatic candidate could not be skipped")
	}
	unchanged := f.refresh(t, body)
	if unchanged.Candidates[0].ChangeKind != "unchanged" {
		t.Fatal("stable source was not classified unchanged")
	}
	pending := f.refresh(t, body+sourcePreviewURI("b", "B", "b.example.invalid", "SYNTHETIC_SAFE_B"))
	old, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, unchanged.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || old.State != "superseded" {
		t.Fatal("successful refresh did not supersede pending preview")
	}
	doc, err := f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.sources.Update(f.h.env.ctx, source.Mutation{ScopeID: f.h.env.scope, PrincipalID: f.actor, ResourceID: f.sourceID,
		ExpectedRevision: doc.Metadata.Revision, Name: "Edited source", Tags: doc.Metadata.Tags, Enabled: true, Config: doc.Source, RequestID: "source-edit-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(pending, previewDecision(pending.Candidates[0], "skip"))); !errors.Is(err, imports.ErrStateConflict) {
		t.Fatal("edited source left its previous preview committable")
	}
	doc, err = f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := f.h.sources.EnqueueRefresh(f.h.env.ctx, source.RefreshRequest{ScopeID: f.h.env.scope, SourceID: f.sourceID, ExpectedRevision: doc.Metadata.Revision, RequestID: "schedule"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.h.queue.Claim(f.h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.SourceRefresh}})
	if err != nil || lease == nil || lease.Job.ID != job.ID {
		t.Fatal("scheduled source job not claimable")
	}
	result, commit, err := f.h.sources.HandleRefresh(f.h.env.ctx, *lease)
	if err != nil || result.State != jobs.Succeeded {
		t.Fatal("scheduled source handler failed")
	}
	if _, err := f.h.queue.CompleteTx(f.h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
	doc, err = f.h.sources.Head(f.h.env.ctx, f.h.env.scope, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if count := importCount(t, f.h.env, `SELECT count(*) FROM public.import_batches b JOIN public.jobs j ON j.scope_id=b.scope_id AND j.id=b.job_id WHERE b.id=$1 AND b.actor_id IS NULL AND j.batch_id=$2`, dbID(doc.Source.LatestPreviewBatchID), dbID(f.sourceID)); count != 1 {
		t.Fatal("scheduled preview actor or job association invalid")
	}
	expiring, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, doc.Source.LatestPreviewBatchID, imports.PageOptions{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.env.admin.Exec(f.h.env.ctx, `UPDATE public.import_batches SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()-interval '1 day' WHERE id=$1`, dbID(expiring.BatchID)); err != nil {
		t.Fatal(err)
	}
	expired, err := f.imports.Get(f.h.env.ctx, f.h.env.scope, expiring.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || expired.State != "expired" || len(expired.Candidates) != 0 {
		t.Fatal("expired source candidates were not removed")
	}
	if _, err := f.imports.Commit(f.h.env.ctx, f.input(expiring, previewDecision(expiring.Candidates[0], "skip"))); !errors.Is(err, imports.ErrExpired) {
		t.Fatal("expired source preview remained writable")
	}
}

func TestPostgresSourcePreviewLegacyBindConfirmsLatestBaseline(t *testing.T) {
	f := newSourcePreviewFixture(t, "manual")
	first := f.refresh(t, sourcePreviewURI("a", "A", "old-bind.example.invalid", "SYNTHETIC_BIND_OLD"))
	receipt, err := f.imports.Commit(f.h.env.ctx, f.input(first, previewDecision(first.Candidates[0], "create")))
	if err != nil {
		t.Fatal(err)
	}
	id := receipt.Items[0].ResourceID
	// Colliding stable identities require an explicit choice; their first
	// displayed candidate differs from the previously confirmed baseline.
	pending := f.refresh(t, sourcePreviewURI("a", "A", "new-bind.example.invalid", "SYNTHETIC_BIND_NEW")+
		sourcePreviewURI("a", "Collision", "collision.example.invalid", "SYNTHETIC_BIND_COLLISION"))
	candidate := pending.Candidates[0]
	if candidate.ChangeKind != "conflict" || len(candidate.Diagnostics) < 1 {
		t.Fatal("colliding source identity was not surfaced")
	}
	request, err := json.Marshal(map[string]any{"origin_action": "bind", "source_item_id": candidate.SourceItemID, "binding_revision": candidate.BindingRevision})
	if err != nil {
		t.Fatal("bind fixture unavailable")
	}
	bound := requireNode(t, f.h.do(http.MethodPatch, "/api/v1/nodes/"+string(id), string(request), revisionTag(int64(candidate.ExistingRevision)), nil), http.StatusOK)
	if bound.Node.Endpoint.Host != "new-bind.example.invalid" {
		t.Fatal("explicit bind used the previously applied baseline")
	}
	request, err = json.Marshal(map[string]any{"name": "Local after bind", "binding_revision": bound.Binding.BindingRevision})
	if err != nil {
		t.Fatal("overlay fixture unavailable")
	}
	updated := requireNode(t, f.h.do(http.MethodPatch, "/api/v1/nodes/"+string(id), string(request), revisionTag(int64(bound.Metadata.Revision)), nil), http.StatusOK)
	if updated.Node.Endpoint.Host != "new-bind.example.invalid" {
		t.Fatal("confirmed bind did not advance the baseline for subsequent overlays")
	}
}
