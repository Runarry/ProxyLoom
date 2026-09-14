package storage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"net/netip"
	"testing"
	"time"
)

type testPublicResolver struct{}

func (testPublicResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
}
func TestPostgresNetworkFreezeIdempotencyHistoryAndTargetRevision(t *testing.T) {
	h := newPublicationTest(t)
	e := h.env
	service, err := NewNetworkTests(e.store, h.store.jobs, networktest.Resolver{DNS: testPublicResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	a := networktest.Actor{ScopeID: e.scope, ID: h.actor.ID, Key: "target-create"}
	request := networktest.TargetRequest{Name: "synthetic controlled target", Config: networktest.TargetConfig{URL: "https://target.fixture.invalid/check", AllowedTypes: []string{"connectivity", "download_throughput"}, ExpectedResponse: runnerprotocol.HTTPExpectation{StatusCodes: []int{200}, MaxResponseBytes: 20 << 20}, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 20 << 20}, PermissionBasis: "self_owned", RedirectPolicy: "deny", VerifyCertificate: true, Compression: "disabled"}}
	target, err := service.WriteTarget(e.ctx, a, "", 0, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.WriteTarget(e.ctx, a, "", 0, request)
	if err != nil || replayed.ID != target.ID {
		t.Fatal("target replay failed", err)
	}
	var core ir.ID
	for _, b := range service.cores.Builds() {
		if b.Arch == "amd64" && b.Family == ir.Xray {
			core = b.ID
		}
	}
	a.Key = "batch-create"
	input := networktest.Request{Subjects: []networktest.Subject{{Kind: ir.KindNode, ID: h.node.Metadata.ResourceID}}, CoreBuildID: core, Type: "connectivity", TestTargetID: target.ID, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1000}}
	id, err := service.Create(e.ctx, a, input)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := service.Create(e.ctx, a, input)
	if err != nil || id != id2 {
		t.Fatal("batch replay failed", err)
	}
	budgetTotals(t, e, 1000, 0)
	input.Limits.MaxBytes = 2000
	if _, err = service.Create(e.ctx, a, input); !errors.Is(err, catalog.ErrIdempotencyConflict) {
		t.Fatal("changed request replayed", err)
	}
	lease, err := h.store.jobs.Claim(e.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.Connectivity}, CoreBuildIDs: []ir.ID{core}, AvailableSlots: runnerprotocol.Slots{Connectivity: 1}})
	if err != nil || lease == nil {
		t.Fatal("test claim failed", err)
	}
	defer clear(lease.Payload)
	p, err := runnerprotocol.DecodeFrozenPayload(lease.Payload)
	if err != nil || len(p.Dependencies) != 1 || p.Subject.ID != h.node.Metadata.ResourceID || p.TestTarget.Revision != target.Revision {
		t.Fatal("incomplete frozen payload", err)
	}
	bytes := int64(40)
	result := jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass, Metrics: runnerprotocol.Metrics{BodyBytes: &bytes}, Observation: &runnerprotocol.NetworkObservation{Location: "synthetic-runner", ExecutionSHA256: p.Artifact.SHA256, TruncatedBy: "none"}}
	if _, err = h.store.jobs.Complete(e.ctx, lease.Identity, result); err != nil {
		t.Fatal(err)
	}
	items, more, err := service.Results(e.ctx, e.scope, networktest.ResultFilter{SubjectID: h.node.Metadata.ResourceID, Limit: 50})
	if err != nil || len(items) != 1 || more {
		t.Fatal("missing result history", err)
	}
	var item struct {
		Stale    bool   `json:"stale"`
		Location string `json:"location"`
	}
	json.Unmarshal(items[0], &item)
	if item.Stale || item.Location != "synthetic-runner" {
		t.Fatal("wrong result provenance")
	}
	if err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		name := "edited after test"
		_, err := tx.Update(e.ctx, h.node.Metadata.ResourceID, 1, catalog.UpdateInput{Name: name, Tags: []string{}, Enabled: true, Payload: h.node.Payload})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err = service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(items[0], &item)
	if !item.Stale {
		t.Fatal("old result claimed to be current")
	}
	a.Key = "target-edit"
	request.Name = "renamed target"
	updated, err := service.WriteTarget(e.ctx, a, target.ID, 1, request)
	if err != nil || updated.Revision != 2 {
		t.Fatal("target revision failed", err)
	}
	if _, err = service.WriteTarget(e.ctx, a, target.ID, 1, request); !errors.Is(err, catalog.ErrRevisionConflict) {
		t.Fatal("stale target overwrite accepted", err)
	}
	budgetTotals(t, e, 0, 40)
	// Replays survive current revision and DNS changes without reserving again.
	input.Limits.MaxBytes = 1000
	service.resolver = networktest.Resolver{DNS: unavailableTestResolver{}}
	if replay, err := service.Create(e.ctx, aWithKey(a, "batch-create"), input); err != nil || replay != id {
		t.Fatal("replay consulted live DNS or heads", err)
	}
	// Offline validation must not resolve either node or target addresses.
	offline := input
	offline.Type = "config_validate"
	offline.TestTargetID = ""
	offline.Limits.MaxBytes = 0
	if _, err = service.Create(e.ctx, aWithKey(a, "offline-check"), offline); err != nil {
		t.Fatal("offline validation required network", err)
	}
	lease, err = h.store.jobs.Claim(e.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ConfigValidate}, CoreBuildIDs: []ir.ID{core}, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}})
	if err != nil || lease == nil {
		t.Fatal("offline test not queued", err)
	}
	clear(lease.Payload)
	if lease.Job.Subject == nil || lease.Job.Subject.Revision != 2 {
		t.Fatal("job snapshot omitted subject")
	}
	if _, err = h.store.jobs.Complete(e.ctx, lease.Identity, jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass}); err != nil {
		t.Fatal(err)
	}
	service.resolver = networktest.Resolver{DNS: testPublicResolver{}}
	download := input
	download.Type = "download_throughput"
	if _, err = service.Create(e.ctx, aWithKey(a, "lost-download"), download); err != nil {
		t.Fatal(err)
	}
	lease, err = h.store.jobs.Claim(e.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.DownloadThroughput}, CoreBuildIDs: []ir.ID{core}, AvailableSlots: runnerprotocol.Slots{DownloadThroughput: 1}})
	if err != nil || lease == nil {
		t.Fatal("download not queued", err)
	}
	clear(lease.Payload)
	expireJob(t, e, lease.Job.ID)
	if _, err = h.store.jobs.ReapExpired(e.ctx); err != nil {
		t.Fatal(err)
	}
	items, more, err = service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 50})
	if err != nil || len(items) != 3 || more {
		t.Fatal("lost attempt missing from history", err)
	}
	for _, raw := range items {
		if err := apicontract.ValidateDTO("TestResult", raw); err != nil {
			t.Fatal("history contract mismatch", err)
		}
	}
	var last struct {
		Metrics runnerprotocol.Metrics    `json:"metrics"`
		Error   *runnerprotocol.SafeError `json:"error"`
	}
	json.Unmarshal(items[2], &last)
	if last.Error == nil || last.Error.Code != "LEASE_LOST" || last.Metrics.BodyBytes != nil {
		t.Fatal("lost usage invented a sample")
	}
	first, more, err := service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 1})
	if err != nil || !more || len(first) != 1 {
		t.Fatal("history first page", err)
	}
	var position struct {
		ID ir.ID     `json:"result_id"`
		At time.Time `json:"completed_at"`
	}
	json.Unmarshal(first[0], &position)
	second, _, err := service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 50, After: networktest.PagePosition{ID: position.ID, CreatedAt: position.At}})
	if err != nil || len(second) != 2 {
		t.Fatal("history cursor omitted or duplicated rows", err)
	}
	before, _, err := service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 50, Until: position.At.Format(time.RFC3339Nano)})
	if err != nil || len(before) != 0 {
		t.Fatal("until is not exclusive", err)
	}
	current, _, err := service.Results(e.ctx, e.scope, networktest.ResultFilter{Limit: 50, SubjectRevision: 2})
	if err != nil || len(current) != 2 {
		t.Fatal("subject revision filter", err)
	}
	budgetTotals(t, e, 0, 1040)
}

type unavailableTestResolver struct{}

func (unavailableTestResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return nil, errors.New("synthetic DNS unavailable")
}
func aWithKey(a networktest.Actor, key string) networktest.Actor { a.Key = key; return a }
