package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"sync"
	"testing"
	"time"
)

type publicationTest struct {
	env           *postgresEnv
	store         *Subscriptions
	actor         subscriptions.Actor
	profile, node ir.Resource
	keys          []string
}

func newPublicationTest(t *testing.T) *publicationTest {
	t.Helper()
	return publicationTestEnv(t, newPostgres(t, true))
}
func publicationTestEnv(t *testing.T, e *postgresEnv) *publicationTest {
	t.Helper()
	j, err := NewJobs(e.runtime, e.box)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSubscriptions(e.store, j, bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = s.EnsureCoreBuilds(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err = e.store.EnsureBuiltinClientPresets(e.ctx, e.scope); err != nil {
		t.Fatal(err)
	}
	node := mustCreate(t, e, catalog.CreateInput{Name: "publication-node", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	routing := mustCreate(t, e, catalog.CreateInput{Name: "publication-routing", Tags: []string{}, Enabled: true, Payload: &ir.RoutingProfile{SchemaVersion: 1, Rules: []ir.RoutingRule{}, Final: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: node.Metadata.ResourceID}, DomainResolutionMode: ir.PreserveDomain}})
	dns := mustCreate(t, e, catalog.CreateInput{Name: "publication-dns", Tags: []string{}, Enabled: true, Payload: &ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}}, Resolvers: []ir.DNSResolver{{ResolverID: "local", Kind: ir.DNSLocal}}, Rules: []ir.DNSRule{}, FinalResolver: "local"}})
	presets, err := e.store.listRouting(e.ctx, e.scope, ir.KindClientPreset, catalog.RoutingListOptions{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	p := ir.SubscriptionProfile{SchemaVersion: 1, Members: ir.SubscriptionMembers{IncludeIDs: []ir.ID{node.Metadata.ResourceID}, ExcludeIDs: []ir.ID{}, Selector: ir.TagSelector{AllTags: []string{}, AnyTags: []string{}, NoneTags: []string{}}}, RoutingProfileID: routing.Metadata.ResourceID, DNSProfileID: dns.Metadata.ResourceID, Targets: []ir.SubscriptionTarget{}, PublishPolicy: "strict_all_targets"}
	keys := []string{}
	for _, b := range s.cores.Builds() {
		if b.Arch != "amd64" {
			continue
		}
		for _, preset := range presets.Items {
			v := preset.Payload.(*ir.ClientPreset)
			if v.CoreFamily == b.Family && !v.ControlAPI.Enabled {
				key := string(b.Family) + "-default"
				keys = append(keys, key)
				p.Targets = append(p.Targets, ir.SubscriptionTarget{Key: key, CoreBuildID: b.ID, ClientPresetID: preset.Metadata.ResourceID, Format: v.Format, PolicyOverrides: []ir.PolicyOverride{}})
			}
		}
	}
	profile := mustCreate(t, e, catalog.CreateInput{Name: "publication-profile", Tags: []string{}, Enabled: true, Payload: &p})
	return &publicationTest{env: e, store: s, actor: subscriptions.Actor{ScopeID: e.scope, ID: jobs.NewID()}, profile: profile, node: node, keys: keys}
}
func (h *publicationTest) compile(t *testing.T) subscriptions.Batch {
	t.Helper()
	b, err := h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, h.profile.Metadata.Revision, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil {
		t.Fatal("freeze:", err)
	}
	lease, err := h.store.jobs.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.Compile}})
	if err != nil || lease == nil {
		t.Fatal("compile lease:", err)
	}
	result, commit, err := h.store.HandleCompile(h.env.ctx, *lease)
	if err != nil {
		t.Fatal("compile handler:", err)
	}
	if _, err = h.store.jobs.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal("compile completion:", err)
	}
	b, err = h.store.GetBatch(h.env.ctx, h.actor, b.BatchID)
	if err != nil {
		t.Fatal("batch:", err)
	}
	if b.State != "validating" {
		t.Fatalf("expected validating, got %s; diagnostics=%v", b.State, b.Diagnostics)
	}
	return b
}

// Only transaction semantics are simulated here. Real mTLS/kernel acceptance
// has a separate suite; these synthetic verdicts are not native evidence.
func (h *publicationTest) validate(t *testing.T, b subscriptions.Batch, fail bool) subscriptions.Batch {
	t.Helper()
	builds := []ir.ID{}
	for _, o := range b.Outputs {
		builds = append(builds, o.CoreBuildID)
	}
	for _, output := range b.Outputs {
		if output.State == "ready" {
			continue
		}
		lease, err := h.store.jobs.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ConfigValidate}, CoreBuildIDs: builds})
		if err != nil || lease == nil {
			t.Fatal("validation lease:", err)
		}
		payload, err := runnerprotocol.DecodeFrozenPayload(lease.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Artifact.ByteLength == 0 {
			t.Fatal("missing actual artifact")
		}
		verdict := jobs.Verdict("pass")
		if fail {
			verdict = jobs.Verdict("fail")
			fail = false
		}
		if _, err = h.store.jobs.Complete(h.env.ctx, lease.Identity, jobs.Result{State: jobs.Succeeded, Verdict: verdict}); err != nil {
			t.Fatal("validation completion:", err)
		}
	}
	result, err := h.store.GetBatch(h.env.ctx, h.actor, b.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (h *publicationTest) publish(t *testing.T, b subscriptions.Batch, generation int64) subscriptions.Publication {
	t.Helper()
	request := subscriptions.PublishRequest{BatchID: b.BatchID, ExpectedGeneration: subscriptions.Counter(generation), EffectivePreviewHash: b.EffectivePreviewHash}
	request.Confirmation.Acknowledged = true
	p, err := h.store.Publish(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, h.profile.Metadata.Revision, request)
	if err != nil {
		t.Fatal("publish:", err)
	}
	return p
}
func TestPostgresPublicationAtomicSnapshotRevocationAndRollback(t *testing.T) {
	h := newPublicationTest(t)
	b := h.validate(t, h.compile(t), false)
	p := h.publish(t, b, 0)
	a := h.actor
	a.Key = "issue-one"
	issued, err := h.store.IssueToken(h.env.ctx, a, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "test", AllowedTargets: h.keys})
	if err != nil || issued.Token == "" {
		t.Fatal("token issue:", err)
	}
	replay, err := h.store.IssueToken(h.env.ctx, a, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "test", AllowedTargets: h.keys})
	if err != nil || !replay.Replayed || replay.Token != "" {
		t.Fatal("one-time replay failed")
	}
	before, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0])
	if err != nil || len(before.Bytes) == 0 {
		t.Fatal("download:", err)
	}
	var selected subscriptions.PublishedTarget
	for _, o := range p.Targets {
		if o.TargetKey == h.keys[0] {
			selected = o
		}
	}
	stored, err := h.store.artifact(h.env.ctx, h.env.runtime, h.env.scope, selected.ArtifactID)
	if err != nil || !bytes.Equal(stored, before.Bytes) {
		t.Fatal("download did not return exact frozen bytes")
	}
	clear(stored)
	var next ir.Resource
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		var err error
		next, err = tx.Update(h.env.ctx, h.node.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "renamed", Tags: []string{}, Enabled: true, Payload: h.node.Payload})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0])
	if err != nil || !bytes.Equal(before.Bytes, after.Bytes) {
		t.Fatal("ordinary editing changed published bytes")
	}
	rollback, err := h.store.Rollback(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, subscriptions.RollbackRequest{PublicationID: p.PublicationID, ExpectedGeneration: 1})
	if err != nil || rollback.Generation != 2 {
		t.Fatal("safe rollback:", err)
	}
	rotated := *next.Payload.(*ir.Node)
	rotated.Auth = &ir.PasswordAuth{Kind: ir.AuthPassword, Password: "SYNTHETIC_M2_ROTATED_CREDENTIAL"}
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(h.env.ctx, next.Metadata.ResourceID, 2, catalog.UpdateInput{Name: next.Metadata.Name, Tags: []string{}, Enabled: true, Payload: &rotated})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Download(h.env.ctx, issued.Token, h.keys[0]); !errors.Is(err, subscriptions.ErrBlocked) {
		t.Fatal("old credentials remained downloadable")
	}
	if _, err = h.store.Rollback(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, subscriptions.RollbackRequest{PublicationID: p.PublicationID, ExpectedGeneration: 2}); !errors.Is(err, subscriptions.ErrBlocked) {
		t.Fatal("rollback revived old credentials")
	}
	if _, err = h.store.RevokeToken(h.env.ctx, h.actor, issued.Metadata.TokenID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Download(h.env.ctx, issued.Token, h.keys[0]); !errors.Is(err, subscriptions.ErrToken) {
		t.Fatal("revoked token did not fail generically")
	}
}
func TestPostgresPublicationObsoleteFailureAndConcurrentPublish(t *testing.T) {
	h := newPublicationTest(t)
	b := h.validate(t, h.compile(t), false)
	request := subscriptions.PublishRequest{BatchID: b.BatchID, EffectivePreviewHash: b.EffectivePreviewHash}
	request.Confirmation.Acknowledged = true
	other := h.actor
	other.ID = jobs.NewID()
	if _, err := h.store.Publish(h.env.ctx, other, h.profile.Metadata.ResourceID, 1, request); !errors.Is(err, subscriptions.ErrConfirmation) {
		t.Fatal("unseen preview accepted")
	}
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.store.Publish(context.Background(), h.actor, h.profile.Metadata.ResourceID, 1, request)
			success <- err == nil
		}()
	}
	wg.Wait()
	close(success)
	n := 0
	for ok := range success {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatal("concurrent first publish did not elect one winner")
	}
	failed := h.validate(t, h.compile(t), true)
	if failed.State != "failed" {
		t.Fatal("negative native result did not fail entire batch")
	}
	head, err := h.store.Head(h.env.ctx, h.env.scope, h.profile.Metadata.ResourceID)
	if err != nil || head.Generation != 1 || head.State != "active" {
		t.Fatal("failed new batch damaged old publication")
	}
	stale := h.compile(t)
	mustCreate(t, h.env, catalog.CreateInput{Name: "unrelated", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	stale, err = h.store.GetBatch(h.env.ctx, h.actor, stale.BatchID)
	if err != nil || stale.State != "obsolete" {
		t.Fatal("catalog edit failed to obsolete batch")
	}
	cores, err := h.store.Cores(h.env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, core := range cores {
		if core.Architecture == "amd64" {
			if _, err = h.store.DisableCore(h.env.ctx, h.actor, core.CoreBuildID, 1); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err = h.store.EnsureCoreBuilds(h.env.ctx); err != nil {
		t.Fatal(err)
	}
	head, err = h.store.Head(h.env.ctx, h.env.scope, h.profile.Metadata.ResourceID)
	if err != nil || head.State != "blocked" {
		t.Fatal("disabled build was re-enabled or old publication remained active")
	}
}

func TestPostgresSubscriptionSelectionReferencesAndImmutableBatch(t *testing.T) {
	h := newPublicationTest(t)
	extra := mustCreate(t, h.env, catalog.CreateInput{Name: "not-selected", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	b := h.compile(t)
	for _, d := range b.Dependencies {
		if d.ResourceID == extra.Metadata.ResourceID {
			t.Fatal("empty selector selected unrelated credentials")
		}
	}
	stored, err := h.store.readBatch(h.env.ctx, h.env.runtime, h.env.scope, b.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(stored.Frozen.Input)
	if bytes.Contains(wire, []byte(extra.Metadata.ResourceID)) {
		t.Fatal("snapshot leaked unrelated member")
	}
	if _, err = h.env.runtime.Exec(h.env.ctx, `UPDATE public.compile_batches SET envelope='{}' WHERE id=$1`, dbID(b.BatchID)); err == nil {
		t.Fatal("runtime role can rewrite frozen input")
	}
	refs, err := h.env.store.References(h.env.ctx, h.env.scope, h.node.Metadata.ResourceID, catalog.ReferenceOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs.Items {
		if ref.SourceID == h.profile.Metadata.ResourceID {
			found = true
		}
	}
	if !found {
		t.Fatal("subscription member reverse reference missing")
	}
}

func TestPostgresSubscriptionTwoProfilesAndExcludedChainDependency(t *testing.T) {
	h := newPublicationTest(t)
	b := mustCreate(t, h.env, catalog.CreateInput{Name: "second exit", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	chain := mustCreate(t, h.env, catalog.CreateInput{Name: "two-hop subscription", Tags: []string{}, Enabled: true, Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: h.node.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})
	p := h.profile.Payload.(*ir.SubscriptionProfile).Clone()
	p.Members.IncludeIDs = []ir.ID{chain.Metadata.ResourceID}
	second := mustCreate(t, h.env, catalog.CreateInput{Name: "second subscription", Tags: []string{}, Enabled: true, Payload: &p})
	first, err := h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	next, err := h.store.Compile(h.env.ctx, h.actor, second.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range first.Dependencies {
		if d.ResourceID == b.Metadata.ResourceID || d.ResourceID == chain.Metadata.ResourceID {
			t.Fatal("independent profiles mixed membership")
		}
	}
	found := false
	for _, d := range next.Dependencies {
		if d.ResourceID == b.Metadata.ResourceID {
			found = true
			if d.Inclusion != "automatic" {
				t.Fatal("chain hop not disclosed as automatic")
			}
		}
	}
	if !found {
		t.Fatal("chain exit dependency missing")
	}
	p.Members.ExcludeIDs = []ir.ID{b.Metadata.ResourceID}
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(h.env.ctx, second.Metadata.ResourceID, 1, catalog.UpdateInput{Name: second.Metadata.Name, Tags: []string{}, Enabled: true, Payload: &p})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.store.Compile(h.env.ctx, h.actor, second.Metadata.ResourceID, 2, subscriptions.CompileRequest{TargetKeys: h.keys})
	var diags ir.Diagnostics
	if !errors.As(err, &diags) || len(diags) == 0 || diags[0].Code != "DEPENDENCY_EXCLUDED" {
		t.Fatal("excluded chain dependency did not block compile")
	}
}

func TestPostgresPublicationFencingReplayAndRestart(t *testing.T) {
	h := newPublicationTest(t)
	h.actor.Key = "persistent-compile"
	b, err := h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild process-owned repositories after the freeze transaction, before
	// any worker has claimed the compile. Only PostgreSQL retains the work.
	frozenQueue, err := NewJobs(h.env.runtime, h.env.box)
	if err != nil {
		t.Fatal(err)
	}
	afterFreeze, err := NewSubscriptions(h.env.store, frozenQueue, bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(afterFreeze.Close)
	h.store = afterFreeze
	lease, err := h.store.jobs.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.Compile}})
	if err != nil || lease == nil {
		t.Fatal("compile lease failed")
	}
	result, commit, err := h.store.HandleCompile(h.env.ctx, *lease)
	if err != nil {
		t.Fatal(err)
	}
	stale := lease.Identity
	stale.LeaseSeq++
	if _, err = h.store.jobs.CompleteTx(h.env.ctx, stale, result, commit); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale compile completion accepted")
	}
	var count int
	if err = h.env.runtime.QueryRow(h.env.ctx, `SELECT count(*) FROM public.compile_outputs WHERE batch_id=$1`, dbID(b.BatchID)).Scan(&count); err != nil || count != 0 {
		t.Fatal("fenced callback wrote outputs")
	}
	if _, err = h.store.jobs.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
	receipt, err := h.store.jobs.CompleteTx(h.env.ctx, lease.Identity, result, commit)
	if err != nil || !receipt.Replayed {
		t.Fatal("duplicate completion reran callback")
	}
	// A completed first target and two queued targets survive another restart
	// while validation is in progress. No output or child job is regenerated.
	coreIDs := []ir.ID{}
	for _, build := range h.store.cores.Builds() {
		if build.Arch == "amd64" {
			coreIDs = append(coreIDs, build.ID)
		}
	}
	validated, err := h.store.jobs.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ConfigValidate}, CoreBuildIDs: coreIDs})
	if err != nil || validated == nil {
		t.Fatal("first validation lease unavailable")
	}
	if _, err = h.store.jobs.Complete(h.env.ctx, validated.Identity, jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass}); err != nil {
		t.Fatal(err)
	}
	queue, err := NewJobs(h.env.runtime, h.env.box)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSubscriptions(h.env.store, queue, bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	h.store = restarted
	replayed, err := h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil || replayed.BatchID != b.BatchID || len(replayed.Outputs) != 3 {
		t.Fatal("restarted compile lost its durable idempotent outputs")
	}
	passed := 0
	for _, output := range replayed.Outputs {
		if output.State == "ready" {
			passed++
		}
	}
	if passed != 1 || replayed.State != "validating" {
		t.Fatal("restart lost partial validation progress")
	}
	b = h.validate(t, replayed, false)
	h.actor.Key = ""
	h.publish(t, b, 0)
	issued, err := h.store.IssueToken(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "database-outage", AllowedTargets: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	h.env.runtime.Close()
	if _, err = h.store.Download(context.Background(), issued.Token, h.keys[0]); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatal("database loss did not fail closed")
	}
}

func TestPostgresPublicationConcurrentSwitchAndCommittedRevocation(t *testing.T) {
	h := newPublicationTest(t)
	first := h.validate(t, h.compile(t), false)
	h.publish(t, first, 0)
	issued, err := h.store.IssueToken(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "one-target", AllowedTargets: h.keys[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Download(h.env.ctx, issued.Token, h.keys[1]); !errors.Is(err, subscriptions.ErrToken) {
		t.Fatal("token crossed target authorization")
	}
	second := h.validate(t, h.compile(t), false)
	request := subscriptions.PublishRequest{BatchID: second.BatchID, ExpectedGeneration: 1, EffectivePreviewHash: "changed"}
	request.Confirmation.Acknowledged = true
	if _, err := h.store.Publish(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, request); !errors.Is(err, subscriptions.ErrConfirmation) {
		t.Fatal("altered preview hash accepted")
	}
	request.EffectivePreviewHash = second.EffectivePreviewHash
	var wg sync.WaitGroup
	errorsOut := make(chan error, 3)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				download, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0])
				if err != nil {
					errorsOut <- err
					return
				}
				clear(download.Bytes)
				var count int
				err = h.env.runtime.QueryRow(h.env.ctx, `SELECT count(*) FROM public.publication_heads h JOIN public.publications p ON p.id=h.publication_id JOIN public.compile_outputs o ON o.batch_id=p.batch_id WHERE h.scope_id=$1 AND h.profile_id=$2`, dbID(h.env.scope), dbID(h.profile.Metadata.ResourceID)).Scan(&count)
				if err != nil || count != 3 {
					errorsOut <- errors.New("head exposed incomplete target set")
					return
				}
			}
		}()
	}
	if _, err = h.store.Publish(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, 1, request); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Fatal(err)
	}
	if _, err = h.store.RevokeToken(h.env.ctx, h.actor, issued.Metadata.TokenID, 1); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0])
			if !errors.Is(err, subscriptions.ErrToken) || len(d.Bytes) != 0 {
				t.Error("request started after revocation received configuration")
			}
		}()
	}
	wg.Wait()
	expiry := time.Now().Add(time.Minute)
	expiring, err := h.store.IssueToken(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "expiry", AllowedTargets: h.keys, ExpiresAt: &expiry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.env.admin.Exec(h.env.ctx, `UPDATE public.subscription_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, dbID(expiring.Metadata.TokenID)); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Download(h.env.ctx, expiring.Token, h.keys[0]); !errors.Is(err, subscriptions.ErrToken) {
		t.Fatal("expired token remained authorized")
	}
}

func TestPostgresPublicationStaleAndResourceRevocation(t *testing.T) {
	f := newSourcePreviewFixture(t, "safe_updates")
	f.refresh(t, sourcePreviewURI("m2-stale", "M2 bound node", "example.invalid", "EXAMPLE_M2_STALE"))
	h := publicationTestEnv(t, f.h.env)
	var bound ir.ID
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT node_id::text FROM public.node_bindings WHERE scope_id=$1 LIMIT 1`, dbID(h.env.scope)).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	p := h.profile.Payload.(*ir.SubscriptionProfile).Clone()
	p.Members.IncludeIDs = []ir.ID{bound}
	err := h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		var err error
		h.profile, err = tx.Update(h.env.ctx, h.profile.Metadata.ResourceID, 1, catalog.UpdateInput{Name: h.profile.Metadata.Name, Tags: []string{}, Enabled: true, Payload: &p})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	b := h.validate(t, h.compile(t), false)
	published := h.publish(t, b, 0)
	issued, err := h.store.IssueToken(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "stale", AllowedTargets: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	f.refresh(t, sourcePreviewURI("m2-replacement", "Replacement", "replacement.example.invalid", "EXAMPLE_M2_NEXT"))
	var state string
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT state FROM public.node_bindings WHERE scope_id=$1 AND node_id=$2`, dbID(h.env.scope), dbID(bound)).Scan(&state); err != nil || state != "stale" {
		t.Fatal("source refresh did not produce the required stale fixture")
	}
	if d, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0]); err != nil || len(d.Bytes) == 0 {
		t.Fatal("default stale handling revoked a safe old publication")
	}
	if _, err = h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, h.profile.Metadata.Revision, subscriptions.CompileRequest{TargetKeys: h.keys}); err == nil {
		t.Fatal("stale manual member entered new publication")
	}
	r, err := h.env.store.Head(h.env.ctx, h.env.scope, bound)
	if err != nil {
		t.Fatal(err)
	}
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(h.env.ctx, bound, r.Metadata.Revision, catalog.UpdateInput{Name: r.Metadata.Name, Tags: r.Metadata.Tags, Enabled: false, Payload: r.Payload})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Download(h.env.ctx, issued.Token, h.keys[0]); !errors.Is(err, subscriptions.ErrBlocked) {
		t.Fatal("disabled node remained downloadable")
	}
	if _, err = h.store.Rollback(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, h.profile.Metadata.Revision, subscriptions.RollbackRequest{PublicationID: published.PublicationID, ExpectedGeneration: 1}); !errors.Is(err, subscriptions.ErrBlocked) {
		t.Fatal("rollback revived disabled node")
	}
	r, err = h.env.store.Head(h.env.ctx, h.env.scope, bound)
	if err != nil {
		t.Fatal(err)
	}
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error { _, err := tx.Delete(h.env.ctx, bound, r.Metadata.Revision); return err })
	if err != nil {
		t.Fatal(err)
	}
	if d, err := h.store.Download(h.env.ctx, issued.Token, h.keys[0]); !errors.Is(err, subscriptions.ErrBlocked) || len(d.Bytes) != 0 {
		t.Fatal("deleted dependency returned configuration bytes")
	}
}
