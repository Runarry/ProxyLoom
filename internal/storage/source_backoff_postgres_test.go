package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/jobs"
)

func TestPostgresSourceRefreshBackoffAccumulatesCapsAndResets(t *testing.T) {
	for _, interval := range []int{300, 600} {
		t.Run(strconv.Itoa(interval), func(t *testing.T) {
			h := newSourceHTTPAcceptance(t)
			var succeed atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !succeed.Load() {
					// An empty feed is a terminal validation failure, so each
					// iteration exercises the next persisted scheduled attempt.
					w.WriteHeader(http.StatusOK)
					return
				}
				_, _ = io.WriteString(w, "http://backoff.example.invalid:8080#backoff\n")
			}))
			t.Cleanup(upstream.Close)
			body := strings.Replace(sourceBody(t, "backoff", upstream.URL, "manual", true), `"interval_seconds":60`, `"interval_seconds":`+strconv.Itoa(interval), 1)
			created := requireSource(t, h.do(http.MethodPost, "/api/v1/sources", body, "", nil), http.StatusCreated)
			id := created.Metadata.ResourceID
			for _, step := range []struct {
				success        bool
				backoff, delay int
			}{
				{false, 120, 120}, {false, 240, 240}, {false, min(480, interval), min(480, interval)},
				{false, interval, interval}, {false, interval, interval}, {true, 60, interval}, {false, 120, 120},
			} {
				succeed.Store(step.success)
				head, err := h.sources.Head(h.env.ctx, h.env.scope, id)
				if err != nil {
					t.Fatal("could not read source head")
				}
				requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/sources/"+string(id)+"/refresh", "", revisionTag(head.Metadata.Revision), nil), http.StatusAccepted)
				lease, err := h.queue.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.SourceRefresh}})
				if err != nil || lease == nil {
					t.Fatal("refresh not claimable")
				}
				started := time.Now().UTC()
				result, commit, err := h.sources.HandleRefresh(h.env.ctx, *lease)
				if err != nil || commit == nil || (result.State == jobs.Succeeded) != step.success {
					t.Fatalf("unexpected refresh result: state=%s err=%v", result.State, err)
				}
				if _, err := h.queue.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
					t.Fatal("refresh commit failed", err)
				}
				ended := time.Now().UTC()
				var backoff int
				var next time.Time
				if err := h.env.runtime.QueryRow(h.env.ctx, "SELECT backoff_seconds,next_run_at FROM public.source_schedules WHERE source_id=$1", dbID(id)).Scan(&backoff, &next); err != nil {
					t.Fatal("schedule read failed")
				}
				delay := time.Duration(step.delay) * time.Second
				if backoff != step.backoff || next.Before(started.Add(delay-time.Millisecond)) || next.After(ended.Add(delay+time.Millisecond)) {
					t.Fatalf("persisted retry schedule: backoff=%d want=%d delay=%s want=%s", backoff, step.backoff, next.Sub(started), delay)
				}
			}
		})
	}
}
