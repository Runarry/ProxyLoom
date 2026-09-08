package runnercontrol_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crypto/rand"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runner"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/Runarry/ProxyLoom/internal/storage"
	"github.com/Runarry/ProxyLoom/internal/validation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type runnerDatabase struct {
	queue *storage.Jobs
	pool  *pgxpool.Pool
	scope ir.ID
}

func realRunnerDatabase(t *testing.T, ctx context.Context) runnerDatabase {
	t.Helper()
	paths := []string{os.Getenv("PROXYLOOM_TEST_DATABASE_DSN_FILE"), os.Getenv("PROXYLOOM_TEST_MIGRATION_DSN_FILE"), os.Getenv("PROXYLOOM_TEST_ADMIN_DSN_FILE")}
	dsns := make([]string, 3)
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatal("runner acceptance DSN file is unavailable")
		}
		dsns[i] = strings.TrimSpace(string(data))
		clear(data)
	}
	open := func(dsn, database string) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal("invalid acceptance DSN")
		}
		if database != "" {
			cfg.ConnConfig.Database = database
		}
		cfg.MaxConns = 4
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal("acceptance database unavailable")
		}
		return pool
	}
	control := open(dsns[2], "")
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "proxyloom_runner_" + hex.EncodeToString(nonce[:])
	if _, err := control.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER proxyloom_migrator"); err != nil {
		control.Close()
		t.Fatal("isolated runner database creation failed")
	}
	admin := open(dsns[2], name)
	migrator := open(dsns[1], name)
	pool := open(dsns[0], name)
	t.Cleanup(func() {
		pool.Close()
		migrator.Close()
		admin.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if !strings.HasPrefix(name, "proxyloom_runner_") || len(name) != len("proxyloom_runner_")+20 {
			t.Error("unsafe acceptance cleanup target")
			return
		}
		if _, err := control.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("isolated runner database cleanup failed")
		}
		control.Close()
	})
	for _, sql := range []string{
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{name}.Sanitize() + " TO proxyloom, proxyloom_migrator",
		"GRANT USAGE ON SCHEMA public TO proxyloom",
		"GRANT CREATE ON SCHEMA public TO proxyloom_migrator",
		"ALTER DEFAULT PRIVILEGES FOR ROLE proxyloom_migrator IN SCHEMA public GRANT SELECT ON TABLES TO proxyloom",
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatal("isolated runner grants failed")
		}
	}
	if status, err := storage.MigrateUp(ctx, migrator); err != nil || !status.Current {
		t.Fatal("isolated runner migration failed")
	}
	master := bytes.Repeat([]byte{0x11}, 32)
	content := bytes.Repeat([]byte{0x33}, 32)
	box, err := secretbox.New("runner_acceptance", map[string][]byte{"runner_acceptance": master}, content)
	clear(master)
	clear(content)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := storage.NewCatalog(pool, box)
	if err != nil {
		t.Fatal(err)
	}
	scope := ir.ID("75000000-0000-4000-8000-000000000001")
	if err := catalog.EnsureScope(ctx, scope, "Runner synthetic acceptance"); err != nil {
		t.Fatal(err)
	}
	queue, err := storage.NewJobs(pool, box)
	if err != nil {
		t.Fatal(err)
	}
	return runnerDatabase{queue: queue, pool: pool, scope: scope}
}

func TestPostgresMTLSRunnerRealCores(t *testing.T) {
	root := os.Getenv("PROXYLOOM_RUNNER_REAL_CORES")
	fixtures := os.Getenv("PROXYLOOM_RUNNER_FIXTURE_ROOT")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || root == "" || fixtures == "" || os.Getenv("PROXYLOOM_TEST_DATABASE_DSN_FILE") == "" {
		if os.Getenv("PROXYLOOM_REQUIRE_RUNNER_TESTS") == "true" {
			t.Fatal("real runner acceptance environment is incomplete")
		}
		t.Skip("requires Linux, three locked cores and isolated PostgreSQL DSN files")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	db := realRunnerDatabase(t, ctx)
	pki := makePKI(t)
	builds := pinnedBuilds(t)
	cores, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	submit, err := validation.New(db.queue, cores)
	if err != nil {
		t.Fatal(err)
	}
	type expected struct {
		job    jobs.Job
		valid  bool
		family ir.CoreFamily
	}
	var expectedJobs []expected
	for _, build := range builds {
		format := ir.XrayJSON
		contentType := "application/json"
		ext := "json"
		if build.Family == ir.SingBox {
			format = ir.SingBoxJSON
		}
		if build.Family == ir.Mihomo {
			format = ir.MihomoYAML
			contentType = "application/yaml"
			ext = "yaml"
		}
		target := ir.Target{Key: string(build.Family) + "-acceptance", CoreFamily: build.Family, CoreBuildID: build.ID, CoreBuildSHA256: build.BinarySHA256, AdapterVersion: build.AdapterVersion, ClientPresetID: "75000000-0000-4000-8000-000000000002", ClientPresetRevision: 1, Format: format}
		for _, valid := range []bool{true, false} {
			kind := "invalid"
			if valid {
				kind = "valid"
			}
			data, err := os.ReadFile(filepath.Join(fixtures, strings.ReplaceAll(string(build.Family), "-", "")+"."+kind+"."+ext))
			if err != nil {
				t.Fatal("real core fixture missing")
			}
			job, err := submit.Submit(ctx, db.scope, "", target, adapter.Artifact{SnapshotID: jobs.NewID(), TargetKey: target.Key, ContentType: contentType, Bytes: data})
			if err != nil {
				t.Fatalf("%s artifact submission failed: %v", build.Family, err)
			}
			var encrypted []byte
			if err := db.pool.QueryRow(ctx, "SELECT envelope::text FROM public.job_payloads WHERE job_id=$1", string(job.ID)).Scan(&encrypted); err != nil {
				t.Fatal("encrypted artifact missing")
			}
			if bytes.Contains(encrypted, data) {
				t.Fatal("plaintext artifact persisted in queue")
			}
			clear(data)
			clear(encrypted)
			expectedJobs = append(expectedJobs, expected{job: job, valid: valid, family: build.Family})
		}
	}
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{registration(pki, builds)}, Jobs: db.queue})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- control.ServeListener(serverCtx, listener, nil) }()
	t.Cleanup(func() {
		stopServer()
		if err := <-serverDone; err != nil {
			t.Error("runner listener cleanup failed")
		}
	})
	endpoint := "https://" + listener.Addr().String()
	client, err := runner.NewClient(runner.ClientConfig{RunnerID: runnerID, APIURL: endpoint, TLS: pki.client, CoreRoot: root})
	if err != nil {
		t.Fatal("real runner rejected locked binaries")
	}
	runnerCtx, stopRunner := context.WithCancel(ctx)
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- client.Run(runnerCtx) }()
	var peakParentThreads, peakUIDThreads, peakCoreThreads, peakCoreVM atomic.Int64
	observeCtx, stopObserve := context.WithCancel(ctx)
	defer stopObserve()
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-observeCtx.Done():
				return
			case <-tick.C:
				paths, _ := filepath.Glob("/proc/[0-9]*/status")
				total := int64(0)
				for _, path := range paths {
					data, err := os.ReadFile(path)
					if err != nil {
						continue
					}
					fields := map[string]string{}
					for _, line := range strings.Split(string(data), "\n") {
						parts := strings.Fields(line)
						if len(parts) > 1 {
							fields[parts[0]] = parts[1]
						}
					}
					if fields["Uid:"] != "10002" {
						continue
					}
					threads, _ := strconv.ParseInt(fields["Threads:"], 10, 64)
					total += threads
					if fields["Pid:"] == strconv.Itoa(os.Getpid()) {
						storeMax(&peakParentThreads, threads)
					}
					if strings.HasPrefix(fields["Name:"], "memfd:") {
						storeMax(&peakCoreThreads, threads)
						size, _ := strconv.ParseInt(fields["VmSize:"], 10, 64)
						storeMax(&peakCoreVM, size)
					}
				}
				storeMax(&peakUIDThreads, total)
			}
		}
	}()
	t.Cleanup(func() {
		stopObserve()
		<-observed
		t.Logf("resource observations: parent_threads=%d visible_uid_threads=%d core_threads=%d core_virtual_kib=%d", peakParentThreads.Load(), peakUIDThreads.Load(), peakCoreThreads.Load(), peakCoreVM.Load())
	})
	runnerStopped := false
	t.Cleanup(func() {
		if !runnerStopped {
			stopRunner()
			<-runnerDone
		}
	})
	for _, want := range expectedJobs {
		for {
			job, err := db.queue.Get(ctx, db.scope, want.job.ID)
			if err != nil {
				t.Fatal("runner job read failed")
			}
			if job.State.Terminal() {
				if job.Attempt != 1 {
					t.Fatalf("%s checker unexpectedly retried infrastructure", want.family)
				}
				if want.valid && (job.State != jobs.Succeeded || job.Verdict != jobs.Pass) || !want.valid && (job.State != jobs.Failed || job.Error == nil || job.Error.Code != "CORE_CONFIG_INVALID") {
					t.Fatalf("%s valid=%v state=%s verdict=%s safe_error=%v", want.family, want.valid, job.State, job.Verdict, job.Error)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("real runner did not finish bounded leases")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	stopRunner()
	if err := <-runnerDone; err != nil {
		t.Fatal(err)
	}
	runnerStopped = true
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: pki.client}, Timeout: 3 * time.Second}
	defer httpClient.CloseIdleConnections()
	var stored []byte
	if err := db.pool.QueryRow(ctx, "SELECT result FROM public.job_results WHERE job_id=$1", string(expectedJobs[0].job.ID)).Scan(&stored); err != nil {
		t.Fatal("result missing")
	}
	var result runnerprotocol.ResultRequest
	if json.Unmarshal(stored, &result) != nil {
		t.Fatal("stored result invalid")
	}
	var receipt runnerprotocol.Response[runnerprotocol.ResultReceipt]
	if post(t, httpClient, endpoint+"/internal/v1/jobs/"+string(result.JobID)+"/result", result, &receipt) != 200 || !receipt.Data.Replayed || receipt.Data.SettledBytes != 0 {
		t.Fatal("durable result replay failed")
	}
	result.LeaseSeq++
	if post(t, httpClient, endpoint+"/internal/v1/jobs/"+string(result.JobID)+"/result", result, nil) != 409 {
		t.Fatal("durable stale result accepted")
	}
	// Cancellation is observed through the real mTLS heartbeat while the
	// durable attempt is still leased, then settles exactly once without a core
	// starting or any network-byte reservation.
	frozen := makePayload(builds[0], []byte(`{}`))
	encoded, _ := json.Marshal(frozen)
	cancelJob, err := db.queue.Enqueue(ctx, jobs.EnqueueInput{ScopeID: db.scope, Executor: jobs.Runner, Type: jobs.ConfigValidate, CoreBuildID: builds[0].ID, Payload: encoded})
	clear(encoded)
	if err != nil {
		t.Fatal(err)
	}
	report := runnerprotocol.RegisterRequest{RunnerID: runnerID, Platform: "linux", Architecture: "amd64", AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}}
	for _, build := range builds {
		report.Builds = append(report.Builds, runnerprotocol.BuildReport{CoreBuildID: build.ID, BuildSHA256: build.BinarySHA256})
	}
	if post(t, httpClient, endpoint+"/internal/v1/runners/heartbeat", report, nil) != 200 {
		t.Fatal("runner re-registration failed")
	}
	var leased runnerprotocol.Response[runnerprotocol.LeaseData]
	if post(t, httpClient, endpoint+"/internal/v1/jobs/lease", runnerprotocol.LeaseRequest{RunnerID: runnerID, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}}, &leased) != 200 || leased.Data.Lease == nil || leased.Data.Lease.JobID != cancelJob.ID {
		t.Fatal("cancellation lease unavailable")
	}
	current, err := db.queue.Get(ctx, db.scope, cancelJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.queue.Cancel(ctx, db.scope, cancelJob.ID, current.Revision); err != nil {
		t.Fatal("leased cancellation failed")
	}
	lease := leased.Data.Lease
	var heartbeat runnerprotocol.Response[runnerprotocol.HeartbeatData]
	if post(t, httpClient, endpoint+"/internal/v1/jobs/"+string(lease.JobID)+"/heartbeat", runnerprotocol.HeartbeatRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq}, &heartbeat) != 200 || !heartbeat.Data.CancelRequested {
		t.Fatal("mTLS heartbeat lost cancellation")
	}
	canceled := runnerprotocol.ResultRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq, State: "canceled", Error: runnerprotocol.Safe("CANCELED")}
	canceled.ResultHash, _ = runnerprotocol.ResultHash(canceled)
	if post(t, httpClient, endpoint+"/internal/v1/jobs/"+string(lease.JobID)+"/result", canceled, &receipt) != 200 || receipt.Data.SettledBytes != 0 {
		t.Fatal("canceled attempt did not settle")
	}
	var nonzero int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM public.job_results WHERE settled_bytes<>0").Scan(&nonzero); err != nil || nonzero != 0 {
		t.Fatal("configuration check settled network bytes")
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "proxyloom-job-") {
			t.Fatal("real checker left a plaintext workspace")
		}
	}
	t.Log("verified encrypted submission -> registered mTLS claim -> locked checker -> fenced zero-byte result for three positive and three negative artifacts")
}

func storeMax(value *atomic.Int64, next int64) {
	for current := value.Load(); next > current; current = value.Load() {
		if value.CompareAndSwap(current, next) {
			return
		}
	}
}
