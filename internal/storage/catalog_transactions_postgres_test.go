package storage

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

func TestPostgresCatalogConcurrentTransactions(t *testing.T) {
	e := newPostgres(t, true)
	var a, b ir.Resource
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		var err error
		a, err = tx.Create(e.ctx, constraintNodeInput("A"))
		if err != nil {
			return err
		}
		b, err = tx.Create(e.ctx, constraintNodeInput("B"))
		return err
	}); err != nil {
		t.Fatal("could not create transaction fixtures")
	}
	if scope, err := e.store.Scope(e.ctx, e.scope); err != nil || scope.CatalogRevision != 1 {
		t.Fatal("two resource mutations did not share one catalog increment")
	}
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errorsCh <- e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
				_, err := tx.Update(e.ctx, a.Metadata.ResourceID, 1, catalog.UpdateInput{
					Name: "winner", Tags: a.Metadata.Tags, Enabled: true, Payload: a.Payload})
				return err
			})
		}()
	}
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		err := <-errorsCh
		if err == nil {
			winners++
		} else if errors.Is(err, catalog.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal("conditional update returned an unexpected error")
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal("same expected revision did not produce exactly one winner")
	}
	head, err := e.store.Head(e.ctx, e.scope, a.Metadata.ResourceID)
	if err != nil || head.Metadata.Revision != 2 || head.Metadata.SecurityEpoch != 1 {
		t.Fatal("concurrent update changed unexpected revision state")
	}
	baseline, err := e.store.Scope(e.ctx, e.scope)
	if err != nil || baseline.CatalogRevision != 2 {
		t.Fatal("losing transaction advanced the catalog")
	}
	var rolledBack ir.Resource
	err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		var err error
		rolledBack, err = tx.Create(e.ctx, constraintNodeInput("rollback"))
		if err != nil {
			return err
		}
		return catalog.ErrInvalidInput
	})
	if !errors.Is(err, catalog.ErrInvalidInput) {
		t.Fatal("callback failure did not roll back")
	}
	if _, err := e.store.Head(e.ctx, e.scope, rolledBack.Metadata.ResourceID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("rollback retained a created resource")
	}
	// Swallowing a later failure must not commit the earlier successful edit.
	err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		if _, err := tx.Update(e.ctx, a.Metadata.ResourceID, 2, catalog.UpdateInput{
			Name: "must roll back", Tags: head.Metadata.Tags, Enabled: true, Payload: head.Payload}); err != nil {
			return err
		}
		_, _ = tx.Update(e.ctx, b.Metadata.ResourceID, 99, catalog.UpdateInput{
			Name: "stale", Tags: b.Metadata.Tags, Enabled: true, Payload: b.Payload})
		return nil
	})
	if !errors.Is(err, catalog.ErrRevisionConflict) {
		t.Fatal("swallowed mutation failure was committed")
	}
	if current, err := e.store.Head(e.ctx, e.scope, a.Metadata.ResourceID); err != nil || current.Metadata.Revision != 2 || current.Metadata.Name != "winner" {
		t.Fatal("failure latch did not undo prior writes")
	}
	var escaped catalog.Tx
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error { escaped = tx; return nil }); err != nil {
		t.Fatal("empty transaction failed")
	}
	if _, err := escaped.Create(e.ctx, constraintNodeInput("escaped")); !errors.Is(err, catalog.ErrTransactionClosed) {
		t.Fatal("transaction handle escaped its callback")
	}
	if after, err := e.store.Scope(e.ctx, e.scope); err != nil || after != baseline {
		t.Fatal("rollback or empty callback changed scope counters")
	}
}

func TestPostgresCatalogIdempotencyConcurrentReplayConflictAndRollback(t *testing.T) {
	e := newPostgres(t, true)
	request := catalog.IdempotencyRequest{ScopeID: e.scope, PrincipalID: "20000000-0000-4000-8000-000000000001",
		RouteKey: "nodes.create", Key: "single-effect", CanonicalRequest: []byte(`{"password":"` + postgresTestSecret + `"}`)}
	var calls atomic.Int64
	callback := func(tx catalog.Tx) (catalog.Receipt, error) {
		calls.Add(1)
		r, err := tx.Create(e.ctx, catalog.CreateInput{Name: "idempotent", Tags: []string{"idem"}, Enabled: true, Payload: syntheticNode()})
		return createdReceipt(r), err
	}
	type outcome struct {
		result catalog.IdempotencyResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 6)
	for range 6 {
		go func() {
			<-start
			result, err := e.store.ExecuteIdempotent(e.ctx, request, callback)
			results <- outcome{result, err}
		}()
	}
	close(start)
	var receipt catalog.Receipt
	fresh, replayed := 0, 0
	for range 6 {
		got := <-results
		if got.err != nil {
			t.Fatal("concurrent idempotency execution failed")
		}
		if receipt.ResourceID == "" {
			receipt = got.result.Receipt
		}
		if got.result.Receipt != receipt {
			t.Fatal("same request returned different receipts")
		}
		if got.result.Replayed {
			replayed++
		} else {
			fresh++
		}
	}
	if calls.Load() != 1 || fresh != 1 || replayed != 5 {
		t.Fatal("concurrent idempotency produced more than one business effect")
	}
	if scope, err := e.store.Scope(e.ctx, e.scope); err != nil || scope.CatalogRevision != 1 {
		t.Fatal("idempotent replay changed catalog revision")
	}
	secondStore, err := NewCatalog(e.runtime, e.box)
	if err != nil {
		t.Fatal("could not recreate catalog repository")
	}
	if result, err := secondStore.ExecuteIdempotent(e.ctx, request, callback); err != nil || !result.Replayed || result.Receipt != receipt || calls.Load() != 1 {
		t.Fatal("receipt was not persistent across repository instances")
	}
	conflicting := request
	conflicting.CanonicalRequest = []byte(`{"different":true}`)
	if _, err := e.store.ExecuteIdempotent(e.ctx, conflicting, callback); !errors.Is(err, catalog.ErrIdempotencyConflict) {
		t.Fatal("same key with different request was accepted")
	}
	other := ir.ID("20000000-0000-4000-8000-000000000002")
	if err := e.store.EnsureScope(e.ctx, other, "other"); err != nil {
		t.Fatal("could not create conflicting scope")
	}
	conflicting = request
	conflicting.ScopeID = other
	if _, err := e.store.ExecuteIdempotent(e.ctx, conflicting, callback); !errors.Is(err, catalog.ErrIdempotencyConflict) {
		t.Fatal("principal route key uniqueness was incorrectly scoped")
	}
	if scope, err := e.store.Scope(e.ctx, other); err != nil || scope.CatalogRevision != 0 {
		t.Fatal("cross-scope conflict changed business state")
	}
	for _, invalidReceipt := range []bool{false, true} {
		attempt := request
		attempt.Key = "callback-rollback"
		if invalidReceipt {
			attempt.Key = "invalid-receipt"
		}
		var created ir.Resource
		_, err := e.store.ExecuteIdempotent(e.ctx, attempt, func(tx catalog.Tx) (catalog.Receipt, error) {
			var err error
			created, err = tx.Create(e.ctx, constraintNodeInput("rollback"))
			if err != nil {
				return catalog.Receipt{}, err
			}
			if !invalidReceipt {
				return catalog.Receipt{}, catalog.ErrInvalidInput
			}
			r := createdReceipt(created)
			r.HTTPStatus = 200 // A created receipt must be 201.
			return r, nil
		})
		if !errors.Is(err, catalog.ErrInvalidInput) {
			t.Fatal("failed business/receipt was not rejected")
		}
		if _, err := e.store.Head(e.ctx, e.scope, created.Metadata.ResourceID); !errors.Is(err, catalog.ErrNotFound) {
			t.Fatal("failed idempotency retained business write")
		}
		var count int
		if err := e.runtime.QueryRow(e.ctx, "SELECT count(*) FROM public.idempotency_keys WHERE principal_id=$1 AND route_key=$2 AND key=$3", attempt.PrincipalID, attempt.RouteKey, attempt.Key).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed idempotency retained orphan claim")
		}
		if result, err := e.store.ExecuteIdempotent(e.ctx, attempt, callback); err != nil || result.Replayed {
			t.Fatal("rolled-back key could not be retried")
		}
	}
	// A valid first write followed by a swallowed invalid mutation still rolls
	// back the claim and receipt, even though the callback returns a valid receipt.
	latched := request
	latched.Key = "latched-rollback"
	_, err = e.store.ExecuteIdempotent(e.ctx, latched, func(tx catalog.Tx) (catalog.Receipt, error) {
		r, err := tx.Create(e.ctx, constraintNodeInput("latched"))
		if err != nil {
			return catalog.Receipt{}, err
		}
		_, _ = tx.Update(e.ctx, r.Metadata.ResourceID, 99, catalog.UpdateInput{Name: "invalid", Tags: r.Metadata.Tags, Enabled: true, Payload: r.Payload})
		return createdReceipt(r), nil
	})
	if !errors.Is(err, catalog.ErrRevisionConflict) {
		t.Fatal("idempotency ignored a swallowed business failure")
	}
	var latchedCount int
	if err := e.runtime.QueryRow(e.ctx, "SELECT count(*) FROM public.idempotency_keys WHERE key=$1", latched.Key).Scan(&latchedCount); err != nil || latchedCount != 0 {
		t.Fatal("latched failure retained an idempotency key")
	}
	assertNoPersistedSyntheticSecret(t, e)
}

func TestPostgresCatalogRewrapCASPreservesImmutableState(t *testing.T) {
	e := newPostgres(t, true)
	r := mustCreate(t, e, catalog.CreateInput{Name: "rewrap", Tags: []string{"rotation"}, Enabled: true, Payload: syntheticNode()})
	args := dbgen.GetResourceRevisionParams{ScopeID: dbID(e.scope), ResourceID: dbID(r.Metadata.ResourceID), Revision: 1}
	before, err := e.store.q.GetResourceRevision(e.ctx, args)
	if err != nil {
		t.Fatal("could not read wrapping baseline")
	}
	scopeBefore, err := e.store.Scope(e.ctx, e.scope)
	if err != nil {
		t.Fatal("could not read scope baseline")
	}
	box, err := secretbox.New("new", map[string][]byte{"old": bytes.Repeat([]byte{0x11}, 32), "new": bytes.Repeat([]byte{0x22}, 32)}, bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal("could not create rotating keyring")
	}
	rotating, err := NewCatalog(e.runtime, box)
	if err != nil {
		t.Fatal("could not create rotating repository")
	}
	version, err := rotating.WrappingVersion(e.ctx, e.scope, r.Metadata.ResourceID, 1)
	if err != nil || version != 1 {
		t.Fatal("initial wrapping counter was incorrect")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; results <- rotating.RewrapRevision(e.ctx, e.scope, r.Metadata.ResourceID, 1, version) }()
	}
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			winners++
		} else if errors.Is(err, catalog.ErrWrapConflict) {
			conflicts++
		} else {
			t.Fatal("rewrap race returned an unexpected error")
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal("rewrap CAS did not produce one winner")
	}
	after, err := rotating.q.GetResourceRevision(e.ctx, args)
	if err != nil || after.WrapVersion != 2 || !bytes.Equal(before.Envelope, after.Envelope) || !bytes.Equal(before.ContentHmac, after.ContentHmac) || before.SecurityEpoch != after.SecurityEpoch || before.Revision != after.Revision || bytes.Equal(before.Wrapping, after.Wrapping) {
		t.Fatal("rewrap changed immutable state or failed to replace wrapping")
	}
	if scope, err := rotating.Scope(e.ctx, e.scope); err != nil || scope != scopeBefore {
		t.Fatal("rewrap advanced scope counters")
	}
	plain, err := rotating.Revision(e.ctx, e.scope, r.Metadata.ResourceID, 1)
	if err != nil {
		t.Fatal("rotated record could not be opened")
	}
	originalJSON, _ := catalog.Canonical(r)
	rotatedJSON, _ := catalog.Canonical(plain)
	if !bytes.Equal(originalJSON, rotatedJSON) {
		t.Fatal("rewrap changed plaintext")
	}
	newOnly, err := secretbox.New("new", map[string][]byte{"new": bytes.Repeat([]byte{0x22}, 32)}, bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal("could not create retired-key test keyring")
	}
	retired, err := NewCatalog(e.runtime, newOnly)
	if err != nil {
		t.Fatal("could not create retired-key repository")
	}
	if _, err := retired.Head(e.ctx, e.scope, r.Metadata.ResourceID); err != nil {
		t.Fatal("rewrapped record still required old master key")
	}
	if _, err := e.store.Head(e.ctx, e.scope, r.Metadata.ResourceID); !errors.Is(err, catalog.ErrCrypto) {
		t.Fatal("missing wrapping key did not fail closed")
	}
	if err := rotating.RewrapRevision(e.ctx, e.scope, r.Metadata.ResourceID, 1, 1); !errors.Is(err, catalog.ErrWrapConflict) {
		t.Fatal("stale wrapping version was accepted")
	}
	if _, err := rotating.WrappingVersion(e.ctx, "30000000-0000-4000-8000-000000000001", r.Metadata.ResourceID, 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("wrapping lookup crossed scope")
	}
	if version, err := rotating.WrappingVersion(e.ctx, e.scope, r.Metadata.ResourceID, 1); err != nil || version != 2 {
		t.Fatal("failed rotation changed CAS version")
	}
	assertNoPersistedSyntheticSecret(t, e)
}

func TestPostgresCatalogScopedPagination(t *testing.T) {
	e := newPostgres(t, true)
	var nodes []ir.Resource
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		for _, tag := range []string{"a", "b", "c"} {
			input := constraintNodeInput("duplicate name")
			input.Tags = []string{tag, "shared"}
			r, err := tx.Create(e.ctx, input)
			if err != nil {
				return err
			}
			nodes = append(nodes, r)
		}
		return nil
	}); err != nil {
		t.Fatal("could not create paginated resources")
	}
	other := ir.ID("40000000-0000-4000-8000-000000000001")
	if err := e.store.EnsureScope(e.ctx, other, "other"); err != nil {
		t.Fatal("could not create second page scope")
	}
	if err := e.store.Transact(e.ctx, other, func(tx catalog.Tx) error {
		input := constraintNodeInput("duplicate name")
		input.Tags = []string{"shared", "foreign"}
		_, err := tx.Create(e.ctx, input)
		return err
	}); err != nil {
		t.Fatal("could not create other-scope resource")
	}
	seen := map[ir.ID]bool{}
	var after *catalog.Position
	for range 4 {
		page, err := e.store.List(e.ctx, e.scope, catalog.ListOptions{Kind: ir.KindNode, Tag: "shared", After: after, Limit: 1})
		if err != nil || len(page.Items) != 1 {
			t.Fatal("bounded resource page was incorrect")
		}
		id := page.Items[0].Metadata.ResourceID
		if seen[id] || page.Items[0].Metadata.ScopeID != e.scope {
			t.Fatal("pagination repeated or crossed scope")
		}
		seen[id] = true
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	if len(seen) != 3 {
		t.Fatal("resource pagination omitted a tied-timestamp row")
	}
	tags, err := e.store.ListTags(e.ctx, e.scope, catalog.TagOptions{Limit: 2})
	if err != nil || len(tags.Items) != 2 || tags.Items[0] != "a" || tags.Items[1] != "b" || tags.Next != "b" {
		t.Fatal("first tag page was incorrect")
	}
	lastTags, err := e.store.ListTags(e.ctx, e.scope, catalog.TagOptions{After: tags.Next, Limit: 2})
	if err != nil || len(lastTags.Items) != 2 || lastTags.Items[0] != "c" || lastTags.Items[1] != "shared" || lastTags.Next != "" {
		t.Fatal("last tag page crossed scope or omitted tags")
	}
	chain := mustCreate(t, e, constraintChainInput(nodes[0].Metadata.ResourceID, nodes[1].Metadata.ResourceID))
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(e.ctx, chain.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "renamed", Tags: []string{}, Enabled: true, Payload: chain.Payload})
		return err
	}); err != nil {
		t.Fatal("could not create second reference revision")
	}
	refs, err := e.store.References(e.ctx, e.scope, nodes[0].Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true, Limit: 1})
	if err != nil || len(refs.Items) != 1 || refs.Items[0].SourceRevision != 1 || refs.Next == nil {
		t.Fatal("first reference page was incorrect")
	}
	lastRefs, err := e.store.References(e.ctx, e.scope, nodes[0].Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true, After: refs.Next, Limit: 1})
	if err != nil || len(lastRefs.Items) != 1 || lastRefs.Items[0].SourceRevision != 2 || !lastRefs.Items[0].Current || lastRefs.Next != nil {
		t.Fatal("last reference page was incorrect")
	}
	if foreignRefs, err := e.store.References(e.ctx, other, nodes[0].Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true}); err != nil || len(foreignRefs.Items) != 0 {
		t.Fatal("reverse-reference read crossed scope")
	}
	if _, err := e.store.Revision(e.ctx, other, nodes[0].Metadata.ResourceID, 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("historical read crossed scope")
	}
}

func createdReceipt(r ir.Resource) catalog.Receipt {
	return catalog.Receipt{HTTPStatus: 201, ResourceID: r.Metadata.ResourceID, Revision: r.Metadata.Revision, Status: catalog.ReceiptCreated}
}

func assertNoPersistedSyntheticSecret(t *testing.T, e *postgresEnv) {
	t.Helper()
	// Scan bytea directly: PostgreSQL's JSON rendering hex-encodes bytea and
	// would hide an accidentally stored plaintext credential from a text scan.
	rows, err := e.runtime.Query(e.ctx, `SELECT envelope FROM public.resource_revisions
UNION ALL SELECT wrapping FROM public.resource_revision_wrappings
UNION ALL SELECT content_hmac FROM public.resource_revisions
UNION ALL SELECT request_hmac FROM public.idempotency_keys
UNION ALL SELECT convert_to(to_jsonb(r)::text, 'UTF8') FROM public.resources r
UNION ALL SELECT convert_to(to_jsonb(r)::text, 'UTF8') FROM public.idempotency_keys r`)
	if err != nil {
		t.Fatal("could not inspect encrypted persistence")
	}
	defer rows.Close()
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			t.Fatal("could not scan encrypted persistence")
		}
		if bytes.Contains(encoded, []byte(postgresTestSecret)) {
			t.Fatal("synthetic secret persisted outside encryption")
		}
	}
	if rows.Err() != nil {
		t.Fatal("encrypted persistence scan failed")
	}
}
