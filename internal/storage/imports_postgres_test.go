package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/jackc/pgx/v5"
)

const importTestWorker ir.ID = "31000000-0000-4000-8000-000000000001"

func newImportPostgres(t *testing.T) (*postgresEnv, *Imports, ir.ID) {
	t.Helper()
	env := newPostgres(t, true)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(env.ctx), 3*time.Minute)
	t.Cleanup(cancel)
	env.ctx = ctx
	identity, token, _ := newIdentityTest(t, env)
	session := setupIdentityTest(t, env, identity, token)
	queue, err := NewJobs(env.runtime, env.box)
	if err != nil {
		t.Fatal("queue initialization failed")
	}
	store, err := NewImports(env.store, queue)
	if err != nil {
		t.Fatal("import initialization failed")
	}
	return env, store, session.User.ID
}

func importTestText(count int) []byte {
	var out strings.Builder
	for i := range count {
		uri := url.URL{Scheme: "trojan", User: url.User("EXAMPLE_IMPORT_PASSWORD"), Host: fmt.Sprintf("node-%d.example.invalid:443", i), Fragment: fmt.Sprintf("Node-%d", i)}
		out.WriteString(uri.String())
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

func createImportTest(t *testing.T, env *postgresEnv, store *Imports, actor ir.ID, text []byte) imports.Accepted {
	t.Helper()
	accepted, err := store.Create(env.ctx, imports.CreateInput{ScopeID: env.scope, PrincipalID: actor, InputFormat: "uri_list", Text: text})
	if err != nil {
		t.Fatalf("import creation failed: %v", err)
	}
	return accepted
}

func parseImportTest(t *testing.T, env *postgresEnv, store *Imports) jobs.Lease {
	t.Helper()
	lease, err := store.jobs.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: importTestWorker, Types: []jobs.Type{jobs.ImportParse}})
	if err != nil || lease == nil {
		t.Fatal("import worker claim failed")
	}
	result, commit, err := store.HandleParse(env.ctx, *lease)
	if err != nil {
		t.Fatalf("import parsing failed: %v", err)
	}
	if _, err := store.jobs.CompleteTx(env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatalf("import completion failed: %v", err)
	}
	return *lease
}

func importCount(t *testing.T, env *postgresEnv, query string, args ...any) int {
	t.Helper()
	var count int
	if err := env.admin.QueryRow(env.ctx, query, args...).Scan(&count); err != nil {
		t.Fatal("import acceptance count unavailable")
	}
	return count
}

func TestPostgresImports5000CrossPageAtomicConcurrentReplay(t *testing.T) {
	env, store, actor := newImportPostgres(t)
	text := importTestText(5000)
	accepted, err := store.Create(env.ctx, imports.CreateInput{ScopeID: env.scope, PrincipalID: actor, InputFormat: "uri_list", Text: text, IdempotencyKey: "large-import-create"})
	if err != nil {
		t.Fatalf("large import create failed: %v", err)
	}
	again, err := store.Create(env.ctx, imports.CreateInput{ScopeID: env.scope, PrincipalID: actor, InputFormat: "uri_list", Text: text, IdempotencyKey: "large-import-create"})
	if err != nil || !again.Replayed || again.BatchID != accepted.BatchID {
		t.Fatal("create idempotency lost")
	}
	if count := importCount(t, env, "SELECT count(*) FROM public.resources"); count != 0 {
		t.Fatal("preview creation wrote formal nodes")
	}
	if count := importCount(t, env, "SELECT count(*) FROM public.import_batches WHERE raw_envelope::text LIKE '%EXAMPLE_IMPORT_PASSWORD%'"); count != 0 {
		t.Fatal("plaintext raw input stored")
	}
	parseImportTest(t, env, store)
	if count := importCount(t, env, "SELECT count(*) FROM public.resources"); count != 0 {
		t.Fatal("parsing wrote formal nodes")
	}
	if count := importCount(t, env, "SELECT count(*) FROM public.import_candidates WHERE envelope::text LIKE '%EXAMPLE_IMPORT_PASSWORD%'"); count != 0 {
		t.Fatal("plaintext candidate stored")
	}
	// Reconstruct the service to ensure candidate identity and cursor paging are
	// derived from durable records, including the final selection on page 25.
	store, err = NewImports(env.store, store.jobs)
	if err != nil {
		t.Fatal("restart failed")
	}
	var decisions []imports.Decision
	cursor := ""
	var revision int64
	for pages := 0; ; pages++ {
		b, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 200, Cursor: cursor})
		if err != nil {
			t.Fatalf("large preview page failed: %v", err)
		}
		if b.State != "ready" || b.CandidateCount != 5000 || b.ExpiresAt.IsZero() {
			t.Fatal("large preview metadata invalid")
		}
		revision = int64(b.Revision)
		for _, c := range b.Candidates {
			if c.State != "new" || c.Node == nil {
				t.Fatal("valid candidate rejected")
			}
			decisions = append(decisions, imports.Decision{CandidateID: c.CandidateID, Action: "create"})
		}
		cursor = b.NextCursor
		if cursor == "" {
			if pages != 24 {
				t.Fatal("unexpected page boundary")
			}
			break
		}
	}
	if len(decisions) != 5000 {
		t.Fatal("cross page selection lost candidates")
	}
	before, err := env.store.Scope(env.ctx, env.scope)
	if err != nil {
		t.Fatal("catalog revision unavailable")
	}
	input := imports.CommitInput{ScopeID: env.scope, PrincipalID: actor, BatchID: accepted.BatchID, ExpectedRevision: revision, IdempotencyKey: "large-import-commit", RequestID: "import-test-large", Decisions: decisions}
	var wg sync.WaitGroup
	results := make(chan imports.Commit, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.Commit(env.ctx, input)
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("concurrent confirmation failed: %v", err)
		}
	}
	replayed := 0
	var first ir.ID
	for result := range results {
		if len(result.Items) != 5000 || result.Items[4999].Status != "created" {
			t.Fatal("large commit receipt incomplete")
		}
		if result.Replayed {
			replayed++
		}
		if first != "" && first != result.Items[0].ResourceID {
			t.Fatal("replay changed resource identity")
		}
		first = result.Items[0].ResourceID
	}
	if replayed != 1 {
		t.Fatal("concurrent commits did not produce one winner")
	}
	after, err := env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision+1 {
		t.Fatal("large import did not advance catalog once")
	}
	if importCount(t, env, "SELECT count(*) FROM public.resources") != 5000 || importCount(t, env, "SELECT count(*) FROM public.resource_audit_events WHERE action='import.commit'") != 1 {
		t.Fatal("commit atomic rows invalid")
	}
	input.Decisions = append([]imports.Decision{}, decisions...)
	input.Decisions[4999].Action = "skip"
	if _, err := store.Commit(env.ctx, input); !errors.Is(err, imports.ErrIdempotencyConflict) {
		t.Fatal("changed commit request was replayed")
	}
}

func TestPostgresImportsConflictRollbackDuplicateHintsAndValidSelection(t *testing.T) {
	env, store, actor := newImportPostgres(t)
	manualNode := syntheticNode()
	manualNode.Features = ir.Features{}
	base := mustCreate(t, env, catalog.CreateInput{Name: "Existing manual node", Tags: []string{}, Enabled: true, Payload: manualNode})
	text := []byte("trojan://" + postgresTestSecret + "@synthetic.example.invalid:443?alpn=h2%2Chttp%2F1.1#Renamed\n" + string(importTestText(1)) + "not-a-supported-uri\n")
	accepted := createImportTest(t, env, store, actor, text)
	parseImportTest(t, env, store)
	b, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || len(b.Candidates) != 3 {
		t.Fatal("mixed preview unavailable")
	}
	if b.Candidates[2].State != "invalid" || b.Candidates[2].Node != nil {
		t.Fatal("invalid candidate promoted")
	}
	if b.Candidates[0].State != "matched" || b.Candidates[0].ExistingResourceID != base.Metadata.ResourceID {
		t.Fatal("manual-node duplicate hint depended on resource name or identity")
	}
	input := imports.CommitInput{ScopeID: env.scope, PrincipalID: actor, BatchID: accepted.BatchID, ExpectedRevision: int64(b.Revision), RequestID: "import-test-conflict", Decisions: []imports.Decision{{CandidateID: b.Candidates[1].CandidateID, Action: "create"}, {CandidateID: b.Candidates[0].CandidateID, Action: "update", ResourceID: base.Metadata.ResourceID, ExpectedRevision: 2}}}
	if _, err := store.Commit(env.ctx, input); !errors.Is(err, imports.ErrRevisionConflict) {
		t.Fatalf("stale update not rejected: %v", err)
	}
	if importCount(t, env, "SELECT count(*) FROM public.resources") != 1 || importCount(t, env, "SELECT count(*) FROM public.import_commits") != 0 {
		t.Fatal("conflict committed partial writes")
	}
	input.Decisions[1] = imports.Decision{CandidateID: b.Candidates[2].CandidateID, Action: "create"}
	if _, err := store.Commit(env.ctx, input); !errors.Is(err, imports.ErrInvalidInput) {
		t.Fatal("invalid candidate accepted")
	}
	if importCount(t, env, "SELECT count(*) FROM public.resources") != 1 {
		t.Fatal("invalid selection committed earlier items")
	}
	input.Decisions[1] = imports.Decision{CandidateID: b.Candidates[2].CandidateID, Action: "skip"}
	if result, err := store.Commit(env.ctx, input); err != nil || result.Items[1].Status != "skipped" {
		t.Fatalf("valid selection could not commit: %v", err)
	}
	// Fingerprints ignore names, IDs and revisions, and include manually created
	// nodes. Exact duplicate URIs remain two separately selectable candidates.
	duplicate := createImportTest(t, env, store, actor, append(importTestText(1), importTestText(1)...))
	parseImportTest(t, env, store)
	b, err = store.Get(env.ctx, env.scope, duplicate.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || len(b.Candidates) != 2 {
		t.Fatal("duplicate preview unavailable")
	}
	if b.Candidates[0].State != "matched" || b.Candidates[0].ExistingResourceID == "" || b.Candidates[0].MatchMethod != "exact_fingerprint" || b.Candidates[1].State != "conflict" {
		t.Fatal("duplicate hints merged identity or missed prior node")
	}
	if importCount(t, env, "SELECT count(*) FROM public.resources") != 2 {
		t.Fatal("duplicate preview changed catalog")
	}
}

func TestPostgresImportSuggestionDoesNotAutoMerge(t *testing.T) {
	env, store, actor := newImportPostgres(t)
	existing := syntheticNode()
	existing.Auth = &ir.PasswordAuth{Kind: ir.AuthPassword, Password: ir.Secret("SYNTHETIC_OTHER_PASSWORD")}
	mustCreate(t, env, catalog.CreateInput{Name: "Edge", Tags: []string{}, Enabled: true, Payload: existing})
	text := []byte("trojan://" + postgresTestSecret + "@synthetic.example.invalid:443?alpn=h2%2Chttp%2F1.1#Edge\n")
	accepted := createImportTest(t, env, store, actor, text)
	parseImportTest(t, env, store)
	b, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || len(b.Candidates) != 1 {
		t.Fatal("suggestion preview unavailable")
	}
	if b.Candidates[0].State != "new" || b.Candidates[0].ExistingResourceID != "" || b.Candidates[0].MatchMethod != "" {
		t.Fatalf("auth change was merged as identity: %#v", b.Candidates[0])
	}
	found := false
	for _, diagnostic := range b.Candidates[0].Diagnostics {
		if diagnostic.Code == importparse.IdentitySuggestion {
			found = true
		}
	}
	if !found {
		t.Fatal("similar node did not produce a suggestion diagnostic")
	}
}

func TestPostgresImportsLeaseFencingExpirationAndAtomicEnqueue(t *testing.T) {
	env, store, actor := newImportPostgres(t)
	if _, err := env.admin.Exec(env.ctx, "REVOKE INSERT(scope_id,job_id,envelope) ON public.job_payloads FROM proxyloom"); err != nil {
		t.Fatal("could not inject enqueue failure")
	}
	_, err := store.Create(env.ctx, imports.CreateInput{ScopeID: env.scope, PrincipalID: actor, Text: importTestText(1), InputFormat: "uri_list"})
	if err == nil {
		t.Fatal("enqueue failure accepted")
	}
	if importCount(t, env, "SELECT count(*) FROM public.import_batches") != 0 || importCount(t, env, "SELECT count(*) FROM public.jobs") != 0 {
		t.Fatal("enqueue failure left partial batch")
	}
	if _, err := env.admin.Exec(env.ctx, "GRANT INSERT(scope_id,job_id,envelope) ON public.job_payloads TO proxyloom"); err != nil {
		t.Fatal("could not restore enqueue privilege")
	}
	accepted := createImportTest(t, env, store, actor, importTestText(1))
	old, err := store.jobs.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: importTestWorker, Types: []jobs.Type{jobs.ImportParse}})
	if err != nil || old == nil {
		t.Fatal("initial lease missing")
	}
	if parsing, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 1}); err != nil || parsing.State != "parsing" {
		t.Fatal("active parse state was not visible")
	}
	result, commit, err := store.HandleParse(env.ctx, *old)
	if err != nil {
		t.Fatal("initial parse failed")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE public.jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", dbID(old.Job.ID)); err != nil {
		t.Fatal("could not expire synthetic lease")
	}
	newQueue, _ := NewJobs(env.runtime, env.box)
	store, _ = NewImports(env.store, newQueue)
	next, err := newQueue.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: "32000000-0000-4000-8000-000000000001", Types: []jobs.Type{jobs.ImportParse}})
	if err != nil || next == nil || next.Identity.LeaseSeq <= old.Identity.LeaseSeq {
		t.Fatal("restart did not recover lease")
	}
	if _, err := newQueue.CompleteTx(env.ctx, old.Identity, result, commit); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale worker committed preview")
	}
	if importCount(t, env, "SELECT count(*) FROM public.import_candidates") != 0 {
		t.Fatal("stale worker left candidates")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE public.import_batches SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()-interval '1 day' WHERE id=$1", dbID(accepted.BatchID)); err != nil {
		t.Fatal("could not age synthetic batch")
	}
	if changed, err := store.Expire(env.ctx, 10); err != nil || changed != 0 {
		t.Fatal("cleanup deleted valid leased job input")
	}
	result, commit, err = store.HandleParse(env.ctx, *next)
	if err != nil {
		t.Fatal("recovered parse failed")
	}
	if _, err := newQueue.CompleteTx(env.ctx, next.Identity, result, commit); err != nil {
		t.Fatal("recovered completion failed")
	}
	// A submitting transaction holds the same batch row lock; cleanup must skip.
	tx, err := env.runtime.BeginTx(env.ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal("submit lock unavailable")
	}
	defer tx.Rollback(env.ctx)
	if _, err := tx.Exec(env.ctx, "SELECT id FROM public.import_batches WHERE id=$1 FOR UPDATE", dbID(accepted.BatchID)); err != nil {
		t.Fatal("submit lock failed")
	}
	if changed, err := store.Expire(env.ctx, 10); err != nil || changed != 0 {
		t.Fatal("cleanup did not skip submitting batch")
	}
	_ = tx.Rollback(env.ctx)
	if changed, err := store.Expire(env.ctx, 10); err != nil || changed != 1 {
		t.Fatal("cleanup failed to expire idle batch")
	}
	b, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 200})
	if err != nil || b.State != "expired" || len(b.Candidates) != 0 || b.ExpiresAt.IsZero() {
		t.Fatal("expired batch state missing")
	}
	if importCount(t, env, "SELECT count(*) FROM public.import_batches WHERE raw_envelope IS NOT NULL") != 0 {
		t.Fatal("expired raw input retained")
	}
	_, err = store.Commit(env.ctx, imports.CommitInput{ScopeID: env.scope, PrincipalID: actor, BatchID: accepted.BatchID, ExpectedRevision: 2, RequestID: "import-expired", Decisions: []imports.Decision{{CandidateID: "33000000-0000-4000-8000-000000000001", Action: "skip"}}})
	if !errors.Is(err, imports.ErrExpired) {
		t.Fatal("expired import was accepted")
	}
}

func TestPostgresImportsUpdateScopeCursorAuditAndReceiptRetention(t *testing.T) {
	env, store, actor := newImportPostgres(t)
	base := mustCreate(t, env, catalog.CreateInput{Name: "Manual", Tags: []string{"before"}, Enabled: true, Payload: syntheticNode()})
	// Keep the authentication-side user lock while creating and confirming. The
	// import and audit ownership checks must not reverse the user's lock order.
	userLock, err := env.admin.Begin(env.ctx)
	if err != nil {
		t.Fatal("actor lock unavailable")
	}
	defer userLock.Rollback(env.ctx)
	if _, err := userLock.Exec(env.ctx, "SELECT id FROM public.users WHERE id=$1 FOR UPDATE", dbID(actor)); err != nil {
		t.Fatal("actor lock failed")
	}
	bounded, cancel := context.WithTimeout(env.ctx, 10*time.Second)
	defer cancel()
	accepted, err := store.Create(bounded, imports.CreateInput{ScopeID: env.scope, PrincipalID: actor, InputFormat: "uri_list", Text: importTestText(3)})
	if err != nil {
		t.Fatal("create took an inverse actor lock")
	}
	parseImportTest(t, env, store)
	first, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatal("cursor preview unavailable")
	}
	if _, err := store.Get(env.ctx, "12000000-0000-4000-8000-000000000001", accepted.BatchID, imports.PageOptions{Limit: 1}); !errors.Is(err, imports.ErrNotFound) {
		t.Fatal("cross-scope preview disclosed")
	}
	if _, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 1, Cursor: first.NextCursor + "x"}); !errors.Is(err, imports.ErrInvalidInput) {
		t.Fatal("tampered cursor accepted")
	}
	second, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Candidates) != 2 || second.Candidates[0].Index != 1 {
		t.Fatal("cursor lost membership")
	}
	name := "Chosen name"
	tags := []string{"after"}
	input := imports.CommitInput{ScopeID: env.scope, PrincipalID: actor, BatchID: accepted.BatchID, ExpectedRevision: int64(first.Revision), RequestID: "import-update-retention", Decisions: []imports.Decision{
		{CandidateID: first.Candidates[0].CandidateID, Action: "update", ResourceID: base.Metadata.ResourceID, ExpectedRevision: 1, Override: &apicontract.NodePatchRequest{Name: &name, Tags: &tags, Node: &apicontract.NodePatch{Auth: &apicontract.AuthPatch{Kind: ir.AuthPassword, Password: apicontract.ReplaceSecret("EXAMPLE_OVERRIDE_PASSWORD")}}}},
		{CandidateID: second.Candidates[1].CandidateID, Action: "create"},
	}}
	receipt, err := store.Commit(bounded, input)
	if err != nil {
		t.Fatalf("explicit update failed: %v", err)
	}
	_ = userLock.Rollback(env.ctx)
	head, err := env.store.Head(env.ctx, env.scope, base.Metadata.ResourceID)
	if err != nil || head.Metadata.Revision != 2 || head.Metadata.SecurityEpoch != 2 || head.Metadata.Name != name || len(head.Metadata.Tags) != 1 || head.Metadata.Tags[0] != "after" {
		t.Fatal("import update bypassed catalog revision/epoch/metadata semantics")
	}
	if head.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != "EXAMPLE_OVERRIDE_PASSWORD" {
		t.Fatal("explicit credential override was not applied")
	}
	wire, err := json.Marshal(receipt)
	if err != nil || strings.Contains(string(wire), "EXAMPLE_OVERRIDE_PASSWORD") || strings.Contains(string(wire), "node-0") {
		t.Fatal("receipt contained secret-bearing payload")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE public.import_batches SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()-interval '1 day' WHERE id=$1", dbID(accepted.BatchID)); err != nil {
		t.Fatal("could not age confirmed batch")
	}
	if count, err := store.Expire(env.ctx, 10); err != nil || count != 1 {
		t.Fatal("confirmed candidate cleanup failed")
	}
	if after, err := store.Get(env.ctx, env.scope, accepted.BatchID, imports.PageOptions{Limit: 200}); err != nil || after.State != "committed" || len(after.Candidates) != 0 {
		t.Fatal("cleanup removed committed tombstone")
	}
	replayed, err := store.Commit(env.ctx, input)
	if err != nil || !replayed.Replayed || len(replayed.Items) != 2 || replayed.Items[0].ResourceID != receipt.Items[0].ResourceID {
		t.Fatal("retention removed stable commit receipt")
	}
	// Cancelled infrastructure jobs must not leave the preview polling forever.
	canceled := createImportTest(t, env, store, actor, importTestText(1))
	if _, err := store.jobs.Cancel(env.ctx, env.scope, canceled.JobID, 1); err != nil {
		t.Fatal("queued import cancellation failed")
	}
	if failed, err := store.Get(env.ctx, env.scope, canceled.BatchID, imports.PageOptions{Limit: 1}); err != nil || failed.State != "failed" || len(failed.Diagnostics) == 0 {
		t.Fatal("terminal job did not close preview state")
	}
}
