package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
)

func TestPostgresOperationsSettingsRaceAuditAndLimits(t *testing.T) {
	e, importer, actor := newImportPostgres(t)
	s, err := NewOperations(e.store, importer.jobs)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.Settings(e.ctx, e.scope)
	if err != nil || initial.Revision != 1 {
		t.Fatal("default settings", err)
	}
	value := initial
	value.CatalogLimits.MaxNodes = 1
	value.CatalogLimits.MaxImportItems = 1
	value.Quota.DailyDownloadBytes = 1 << 20
	value.CleanupPaused = true
	a := operations.Actor{ScopeID: e.scope, ID: actor, RequestID: "ops-concurrent"}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Update(e.ctx, a, 1, value); results <- err }()
	}
	wg.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, catalog.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatal("settings updates lost optimistic concurrency")
	}
	current, err := s.Settings(e.ctx, e.scope)
	if err != nil || current.Revision != 2 || current.Quota.DailyDownloadBytes != 1<<20 {
		t.Fatal("settings and quota were not atomic", err)
	}
	invalid := current
	invalid.Quota.ConnectivityConcurrency = 5
	if _, err = s.Update(e.ctx, a, 2, invalid); !errors.Is(err, operations.ErrInvalid) {
		t.Fatal("deployment concurrency cap bypassed", err)
	}
	first := mustCreate(t, e, catalog.CreateInput{Name: "first", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "second", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
		return err
	})
	var diags ir.Diagnostics
	if !errors.As(err, &diags) || diags[0].Code != ir.InputLimitExceeded {
		t.Fatal("node cap bypassed", err)
	}
	if err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		if _, err := tx.Delete(e.ctx, first.Metadata.ResourceID, 1); err != nil {
			return err
		}
		_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "replacement", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
		return err
	}); err != nil {
		t.Fatal("node limit did not release deleted slot", err)
	}
	// Admission freezes parse limits: raising them must not change queued input.
	b := createImportTest(t, e, importer, actor, importTestText(2))
	current.CatalogLimits.MaxImportItems = 2
	if _, err = s.Update(e.ctx, a, 2, current); err != nil {
		t.Fatal(err)
	}
	parseImportTest(t, e, importer)
	preview, err := importer.Get(e.ctx, e.scope, b.BatchID, imports.PageOptions{Limit: 20})
	if err != nil || preview.State != "failed" {
		t.Fatal("queued import did not retain its limit", err)
	}
	events, more, err := s.Audit(e.ctx, e.scope, operations.AuditFilter{Action: "settings_update", Limit: 200})
	if err != nil || more || len(events) != 2 {
		t.Fatal("settings audit incomplete", err)
	}
	for _, event := range events {
		if err := apicontract.ValidateDTO("AuditEvent", event); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(event), "1048576") || strings.Contains(string(event), "EXAMPLE_") {
			t.Fatal("audit contains setting or credential values")
		}
	}
	all, _, err := s.Audit(e.ctx, e.scope, operations.AuditFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range all {
		if err = apicontract.ValidateDTO("AuditEvent", event); err != nil {
			t.Fatal("historical audit contract", err)
		}
	}
	var last struct {
		ID      ir.ID  `json:"event_id"`
		Created string `json:"created_at"`
	}
	json.Unmarshal(events[1], &last)
	exclusive, _, err := s.Audit(e.ctx, e.scope, operations.AuditFilter{Action: "settings_update", Until: last.Created, Limit: 200})
	if err != nil || len(exclusive) != 1 {
		t.Fatal("audit until must be exclusive", err)
	}
	overview, err := s.Overview(e.ctx, e.scope)
	if err != nil || overview.Resources["node"] != 1 || !overview.CleanupPaused || overview.Budget.Limit != 1<<20 {
		t.Fatal("overview inconsistent", err)
	}
	raw, _ := json.Marshal(overview)
	if err = apicontract.ValidateDTO("SystemOverview", json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresOperationsCompileUsesFrozenExpansionLimit(t *testing.T) {
	h := newPublicationTest(t)
	s, err := NewOperations(h.env.store, h.store.jobs)
	if err != nil {
		t.Fatal(err)
	}
	settings := operations.Defaults()
	settings.CatalogLimits.MaxOutbounds = 1
	a := operations.Actor{ScopeID: h.env.scope, ID: h.actor.ID, RequestID: "ops-freeze"}
	if _, err = s.Update(h.env.ctx, a, 1, settings); err != nil {
		t.Fatal(err)
	}
	b, err := h.store.Compile(h.env.ctx, h.actor, h.profile.Metadata.ResourceID, h.profile.Metadata.Revision, subscriptions.CompileRequest{TargetKeys: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	settings.CatalogLimits.MaxOutbounds = 2000
	if _, err = s.Update(h.env.ctx, a, 2, settings); err != nil {
		t.Fatal(err)
	}
	lease, err := h.store.jobs.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.Compile}})
	if err != nil || lease == nil {
		t.Fatal("claim", err)
	}
	result, commit, err := h.store.HandleCompile(h.env.ctx, *lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.jobs.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
	final, err := h.store.GetBatch(h.env.ctx, h.actor, b.BatchID)
	if err != nil || final.State != "failed" || len(final.Diagnostics) == 0 {
		t.Fatal("compile ignored frozen expansion limit", err)
	}
	for _, diag := range final.Diagnostics {
		if diag.Code != ir.InputLimitExceeded {
			t.Fatal("unexpected diagnostic", diag.Code)
		}
	}
	// A new batch sees the revised limit and follows the original publication path.
	h.compile(t)
}
