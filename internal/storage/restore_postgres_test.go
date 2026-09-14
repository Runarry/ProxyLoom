package storage

import (
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
)

func TestPostgresRestoreAuthorizationAndUnknownUsageSettlement(t *testing.T) {
	h := newPublicationTest(t)
	e := h.env
	identity, setup, _ := newIdentityTest(t, e)
	session := setupIdentityTest(t, e, identity, setup)
	h.publish(t, h.validate(t, h.compile(t), false), 0)
	issued, err := h.store.IssueToken(e.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "pre-restore token", AllowedTargets: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	q := h.store.jobs
	if _, err = q.Enqueue(e.ctx, networkInput(e, jobs.DownloadThroughput, 200)); err != nil {
		t.Fatal(err)
	}
	lease := claimNetwork(t, e, q, jobs.DownloadThroughput, jobs.NewID())
	if lease == nil {
		t.Fatal("no download lease")
	}
	if _, err = q.Enqueue(e.ctx, networkInput(e, jobs.DownloadThroughput, 100)); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, e, 300, 0)
	prior, err := ControlAuthorizationEpoch(e.ctx, e.runtime)
	if err != nil || prior != "" {
		t.Fatal("fresh registration epoch", err)
	}
	if _, err = ResetRestoredAuthorization(e.ctx, e.runtime); err == nil {
		t.Fatal("runtime can reset restore authorization")
	}
	var epoch string
	if err = e.migrator.QueryRow(e.ctx, `SELECT public.proxyloom_reset_after_restore()::text`).Scan(&epoch); err != nil {
		t.Fatal("restore reset failed", err)
	}
	if epoch == "" {
		t.Fatal("missing new registration epoch")
	}
	current, err := ControlAuthorizationEpoch(e.ctx, e.runtime)
	if err != nil || current != epoch {
		t.Fatal("epoch not durable", err)
	}
	if _, err = identity.Authenticate(e.ctx, session.ID); err == nil {
		t.Fatal("old session survived restore")
	}
	if _, err = h.store.Download(e.ctx, issued.Token, h.keys[0]); err == nil {
		t.Fatal("old token survived restore")
	}
	budgetTotals(t, e, 0, 200)
	if _, err = q.Heartbeat(e.ctx, lease.Identity); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("old lease survived restore", err)
	}
	if count := importCount(t, e, `SELECT count(*) FROM public.jobs WHERE finished_at IS NULL`); count != 0 {
		t.Fatal("restore left unfinished jobs")
	}
	if next := claimNetwork(t, e, q, jobs.DownloadThroughput, jobs.NewID()); next != nil {
		t.Fatal("restore restarted a download")
	}
	next, err := ResetRestoredAuthorization(e.ctx, e.migrator)
	if err != nil || string(next) == epoch {
		t.Fatal("repeated restore reused registration epoch", err)
	}
	budgetTotals(t, e, 0, 200)
	if err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(e.ctx, h.node.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "after restore", Tags: []string{}, Enabled: true, Payload: h.node.Payload})
		return err
	}); err != nil {
		t.Fatal("catalog could not advance after restore", err)
	}
}
