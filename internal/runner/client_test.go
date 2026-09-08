package runner

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

const testRunnerID ir.ID = "74000000-0000-4000-8000-000000000001"
const testJobID ir.ID = "74000000-0000-4000-8000-000000000002"

func clientLease(t *testing.T) *runnerprotocol.Lease {
	t.Helper()
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	var build capability.Build
	for _, b := range catalog.Builds() {
		if b.Family == ir.Xray && b.OS == "linux" && b.Arch == "amd64" {
			build = b
		}
	}
	data := []byte(`{"synthetic_fixture":true}`)
	lease := &runnerprotocol.Lease{JobID: testJobID, Attempt: 1, LeaseSeq: 1, LeaseExpiresAt: time.Now().Add(30 * time.Second), FrozenPayload: runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: "config_validate", Core: runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version, BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: build.AdapterVersion}, Artifact: runnerprotocol.Artifact{ArtifactID: "74000000-0000-4000-8000-000000000003", Format: ir.XrayJSON, SHA256: runnerprotocol.Digest(data), ByteLength: int64(len(data)), ContentBase64: base64.StdEncoding.EncodeToString(data)}, Limits: runnerprotocol.Limits{DurationMS: 15000}, ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "none", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32}}}
	lease.PayloadSHA256, _ = runnerprotocol.PayloadHash(lease.FrozenPayload)
	return lease
}

func testClient(t *testing.T, lease *runnerprotocol.Lease, validate func(context.Context, coreexec.Registry, ir.ID, []byte, time.Duration, coreexec.SandboxPolicy) (coreexec.Result, error), cancelHeartbeat bool, cleaned *atomic.Bool) (*Client, chan runnerprotocol.ResultRequest) {
	t.Helper()
	results := make(chan runnerprotocol.ResultRequest, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any
		switch r.URL.Path {
		case "/internal/v1/jobs/" + string(testJobID) + "/events":
			var request runnerprotocol.EventRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			value = runnerprotocol.EventReceipt{JobID: request.JobID, Attempt: request.Attempt, LeaseSeq: request.LeaseSeq, EventID: request.EventID, Seq: 1}
		case "/internal/v1/jobs/" + string(testJobID) + "/heartbeat":
			value = runnerprotocol.HeartbeatData{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq, LeaseExpiresAt: time.Now().Add(30 * time.Second), CancelRequested: cancelHeartbeat}
		case "/internal/v1/jobs/" + string(testJobID) + "/result":
			var request runnerprotocol.ResultRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if cleaned != nil && !cleaned.Load() {
				t.Error("result arrived before process/workspace cleanup")
			}
			results <- request
			value = runnerprotocol.ResultReceipt{JobID: request.JobID, Attempt: request.Attempt, LeaseSeq: request.LeaseSeq, ResultID: "74000000-0000-4000-8000-000000000004", ResultHash: request.ResultHash}
		default:
			t.Error("unexpected control path")
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runnerprotocol.Response[any]{RequestID: "test", Data: value})
	}))
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	catalog, _ := capability.Load()
	build, _ := catalog.Build(lease.Core.CoreBuildID)
	binary, _ := os.Executable()
	registry := coreexec.MapRegistry{Files: map[ir.ID]string{build.ID: binary}, Builds: map[ir.ID]capability.Build{build.ID: build}}
	client, err := newClient(ClientConfig{RunnerID: testRunnerID, APIURL: server.URL, TLS: &tls.Config{RootCAs: pool, Certificates: server.TLS.Certificates}}, registry, []runnerprotocol.BuildReport{{CoreBuildID: build.ID, BuildSHA256: build.BinarySHA256}}, validate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.http.CloseIdleConnections)
	return client, results
}

func TestLeaseWatchdogStopsBeforeUnrenewedDeadline(t *testing.T) {
	lease := clientLease(t)
	lease.LeaseExpiresAt = time.Now().Add(6 * time.Second)
	var cleaned atomic.Bool
	client, results := testClient(t, lease, func(ctx context.Context, _ coreexec.Registry, _ ir.ID, _ []byte, _ time.Duration, _ coreexec.SandboxPolicy) (coreexec.Result, error) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		cleaned.Store(true)
		return coreexec.Result{Canceled: true}, nil
	}, false, &cleaned)
	started := time.Now()
	if err := client.executeLease(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("watchdog stopped too late: %v", elapsed)
	}
	result := <-results
	if result.Error == nil || result.Error.Code != "LEASE_LOST" {
		t.Fatal("lost lease did not produce safe failure")
	}
}

func TestLeaseDeadlineCannotOutliveLocalRequestBudget(t *testing.T) {
	client := &Client{clockOffset: -time.Hour}
	issued := time.Now().Add(-10 * time.Second)
	if duration := client.stopAfter(time.Now().Add(time.Hour), issued); duration > 15*time.Second || duration < 14*time.Second {
		t.Fatalf("wall clock skew overrode monotonic lease budget: %v", duration)
	}
	if duration := client.stopAfter(time.Now().Add(time.Hour), time.Now().Add(-26*time.Second)); duration >= 0 {
		t.Fatal("delayed response revived an expired local execution budget")
	}
}

func TestCancellationHeartbeatWaitsForCleanup(t *testing.T) {
	lease := clientLease(t)
	var cleaned atomic.Bool
	client, results := testClient(t, lease, func(ctx context.Context, _ coreexec.Registry, _ ir.ID, _ []byte, _ time.Duration, _ coreexec.SandboxPolicy) (coreexec.Result, error) {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond)
		cleaned.Store(true)
		return coreexec.Result{Canceled: true}, nil
	}, true, &cleaned)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := time.Now()
	if err := client.executeLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 7*time.Second {
		t.Fatal("cancellation waited for caller timeout instead of heartbeat")
	}
	result := <-results
	if result.State != "canceled" || result.Error == nil || result.Error.Code != "CANCELED" {
		t.Fatal("server cancellation was not honored")
	}
}

func TestFrozenPayloadAndCoreMismatchNeverExecute(t *testing.T) {
	for _, mutate := range []func(*runnerprotocol.Lease){
		func(l *runnerprotocol.Lease) {
			l.Artifact.ContentBase64 = base64.StdEncoding.EncodeToString([]byte("changed"))
		},
		func(l *runnerprotocol.Lease) { l.PayloadSHA256 = runnerprotocol.Digest([]byte("wrong digest")) },
		func(l *runnerprotocol.Lease) {
			l.Core.BuildSHA256 = runnerprotocol.Digest([]byte("wrong binary"))
			l.PayloadSHA256, _ = runnerprotocol.PayloadHash(l.FrozenPayload)
		},
		func(l *runnerprotocol.Lease) {
			l.ExecutionPolicy.Network = "controlled_target_only"
			l.PayloadSHA256, _ = runnerprotocol.PayloadHash(l.FrozenPayload)
		},
	} {
		lease := clientLease(t)
		client, results := testClient(t, lease, func(context.Context, coreexec.Registry, ir.ID, []byte, time.Duration, coreexec.SandboxPolicy) (coreexec.Result, error) {
			t.Fatal("invalid lease reached checker")
			return coreexec.Result{}, nil
		}, false, nil)
		mutate(lease)
		if err := client.executeLease(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.State != "failed" || result.Error == nil || result.Error.Code != "INVALID_CONFIG" {
			t.Fatal("invalid payload was not rejected")
		}
	}
}

func TestCleanupFailureNeverSettlesOrReusesSlot(t *testing.T) {
	lease := clientLease(t)
	client, results := testClient(t, lease, func(context.Context, coreexec.Registry, ir.ID, []byte, time.Duration, coreexec.SandboxPolicy) (coreexec.Result, error) {
		return coreexec.Result{}, coreexec.ErrWorkspaceCleanup
	}, false, nil)
	if err := client.executeLease(context.Background(), lease); !errors.Is(err, ErrUnsafeCleanup) {
		t.Fatalf("unsafe cleanup was hidden: %v", err)
	}
	select {
	case <-results:
		t.Fatal("uncleaned attempt was settled")
	default:
	}
}
