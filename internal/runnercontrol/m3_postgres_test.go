package runnercontrol_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	"github.com/Runarry/ProxyLoom/internal/runner"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/server"
	"github.com/Runarry/ProxyLoom/internal/storage"
)

// The verifier owns an isolated Docker TEST-NET namespace. These endpoints
// never bind a host public port or use administrator-supplied credentials.
const m3FixtureIP = "192.0.2.2"

func m3SOCKS(t *testing.T, source, onlySource string) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(m3FixtureIP, "0"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	stop := func() { _ = l.Close() }
	t.Cleanup(func() {
		stop()
		mu.Lock()
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Go(func() {
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
				if onlySource != "" && host != onlySource {
					return
				}
				conn.SetDeadline(time.Now().Add(60 * time.Second))
				h := make([]byte, 2)
				if _, err = io.ReadFull(conn, h); err != nil || h[0] != 5 {
					return
				}
				methods := make([]byte, int(h[1]))
				if _, err = io.ReadFull(conn, methods); err != nil {
					return
				}
				if _, err = conn.Write([]byte{5, 0}); err != nil {
					return
				}
				h = make([]byte, 4)
				if _, err = io.ReadFull(conn, h); err != nil || h[0] != 5 || h[1] != 1 || h[3] != 1 {
					return
				}
				addr := make([]byte, 6)
				if _, err = io.ReadFull(conn, addr); err != nil || net.IP(addr[:4]).String() != m3FixtureIP {
					return
				}
				dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
				remote, err := dialer.Dial("tcp", net.JoinHostPort(m3FixtureIP, strconv.Itoa(int(binary.BigEndian.Uint16(addr[4:])))))
				if err != nil {
					return
				}
				defer remote.Close()
				conn.Write([]byte{5, 0, 0, 1, 192, 0, 2, 2, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, conn); remote.Close(); close(done) }()
				io.Copy(conn, remote)
				conn.Close()
				<-done
			})
		}
	})
	return l.Addr().(*net.TCPAddr).Port, stop
}

func TestPostgresM3RealNetwork(t *testing.T) {
	root := os.Getenv("PROXYLOOM_RUNNER_REAL_CORES")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || root == "" || os.Getenv("PROXYLOOM_TEST_DATABASE_DSN_FILE") == "" {
		if os.Getenv("PROXYLOOM_REQUIRE_RUNNER_TESTS") == "true" {
			t.Fatal("M3 acceptance environment incomplete")
		}
		t.Skip("requires controlled Linux TEST-NET and PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	db := realRunnerDatabase(t, ctx)
	pki := makePKI(t)
	builds := pinnedBuilds(t)
	pub, err := storage.NewSubscriptions(db.catalog, db.queue, bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	if err = pub.EnsureCoreBuilds(ctx); err != nil {
		t.Fatal(err)
	}
	tests, err := storage.NewNetworkTests(db.catalog, db.queue, networktest.Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	reg := registration(pki, builds)
	reg.ConnectivitySlots = 4
	reg.ThroughputSlots = 1
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{reg}, Jobs: db.queue})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	controlCtx, stopControl := context.WithCancel(ctx)
	controlDone := make(chan error, 1)
	go func() { controlDone <- control.ServeListener(controlCtx, listener, nil) }()
	startRunner := func() (context.CancelFunc, <-chan error) {
		client, e := runner.NewClient(runner.ClientConfig{RunnerID: runnerID, APIURL: "https://" + address, TLS: pki.client, CoreRoot: root, NetworkEnabled: true, Location: "synthetic-linux-amd64"})
		if e != nil {
			t.Fatal(e)
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- client.Run(runCtx) }()
		return cancel, done
	}
	stopRunner, runnerDone := startRunner()
	defer func() {
		stopRunner()
		stopControl()
		for _, done := range []<-chan error{runnerDone, controlDone} {
			if done == nil {
				continue
			}
			if e := <-done; e != nil && !errors.Is(e, context.Canceled) {
				t.Error("M3 background shutdown failed", e)
			}
		}
	}()
	p1, stopFirst := m3SOCKS(t, "127.0.0.2", "")
	p2, _ := m3SOCKS(t, "127.0.0.3", "127.0.0.2")
	create := func(name string, payload ir.ResourcePayload) ir.Resource {
		var r ir.Resource
		err := db.catalog.Transact(ctx, db.scope, func(tx catalog.Tx) error {
			var err error
			r, err = tx.Create(ctx, catalog.CreateInput{Name: name, Tags: []string{}, Enabled: true, Payload: payload})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	node := func(name string, port int) ir.Resource {
		udp := false
		return create(name, &ir.Node{SchemaVersion: 1, Protocol: ir.SOCKS5, Endpoint: ir.Endpoint{Host: m3FixtureIP, Port: port}, Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}})
	}
	first, exit := node("M3 第一跳", p1), node("M3 最终出口", p2)
	chain := create("M3 完整两跳", &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: first.Metadata.ResourceID}, {NodeID: exit.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed})
	targetListener, err := net.Listen("tcp", net.JoinHostPort(m3FixtureIP, "0"))
	if err != nil {
		t.Fatal(err)
	}
	var downloads, activeDownloads atomic.Int64
	targetHTTP := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return
		}
		if r.URL.Path == "/download" {
			downloads.Add(1)
			activeDownloads.Add(1)
			defer activeDownloads.Add(-1)
			w.Header().Set("Content-Length", strconv.Itoa(64<<20))
			buf := make([]byte, 16384)
			for range 4096 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(25 * time.Millisecond):
				}
				if _, err := w.Write(buf); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		io.WriteString(w, "controlled-ok")
	})}
	go targetHTTP.Serve(targetListener)
	defer targetHTTP.Close()
	targetURL := "http://" + targetListener.Addr().String()
	actor := networktest.Actor{ScopeID: db.scope, ID: jobs.NewID(), Key: "m3-target"}
	request := networktest.TargetRequest{Name: "M3 受控目标", Config: networktest.TargetConfig{URL: targetURL + "/check", AllowedTypes: []string{"connectivity", "download_throughput"}, ExpectedResponse: runnerprotocol.HTTPExpectation{StatusCodes: []int{200}, MaxResponseBytes: 20 << 20}, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 20 << 20}, PermissionBasis: "self_owned", RedirectPolicy: "deny", VerifyCertificate: true, Compression: "disabled"}}
	target, err := tests.WriteTarget(ctx, actor, "", 0, request)
	if err != nil {
		t.Fatal(err)
	}
	actor.Key = "m3-download-target"
	request.Name = "M3 限额下载"
	request.Config.URL = targetURL + "/download"
	downloadTarget, err := tests.WriteTarget(ctx, actor, "", 0, request)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PROXYLOOM_M3_BROWSER_SERVER") == "true" {
		serveM3Browser(t, ctx, db, pub, tests, map[string]any{"node": first.Metadata.ResourceID, "exit": exit.Metadata.ResourceID, "chain": chain.Metadata.ResourceID, "target": target.ID, "download_target": downloadTarget.ID, "target_url": targetURL})
		return
	}
	waitBatch := func(id ir.ID) jobs.Batch {
		for {
			snap, err := db.queue.Snapshot(ctx, db.scope, id)
			if err != nil {
				if errors.Is(err, jobs.ErrUnavailable) && ctx.Err() == nil {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				t.Fatal(err)
			}
			if snap.Batch.State.Terminal() {
				return *snap.Batch
			}
			select {
			case <-ctx.Done():
				t.Fatal("M3 batch timed out")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	for _, build := range builds {
		actor.Key = "m3-three-core-" + string(build.Family)
		id, err := tests.Create(ctx, actor, networktest.Request{Subjects: []networktest.Subject{{Kind: ir.KindNode, ID: first.Metadata.ResourceID}, {Kind: ir.KindChain, ID: chain.Metadata.ResourceID}}, CoreBuildID: build.ID, Type: "connectivity", TestTargetID: target.ID, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1024}})
		if err != nil {
			t.Fatal(err)
		}
		b := waitBatch(id)
		if b.State != jobs.Succeeded || b.Verdict != jobs.Pass {
			for _, j := range b.Children {
				t.Logf("%s: state=%s verdict=%s error=%v", build.Family, j.State, j.Verdict, j.Error)
			}
			t.Fatal("real durable network batch failed")
		}
		t.Logf("PASS: %s single node and complete chain via durable mTLS queue", build.Family)
	}
	// Four leases must coexist and finish on their first attempt on one Runner.
	var concurrentSubjects []networktest.Subject
	for i := 0; i < 4; i++ {
		port, _ := m3SOCKS(t, "127.0.0.2", "")
		r := node("M3 并发 "+strconv.Itoa(i), port)
		concurrentSubjects = append(concurrentSubjects, networktest.Subject{Kind: ir.KindNode, ID: r.Metadata.ResourceID})
	}
	actor.Key = "m3-four-slots"
	concurrentID, err := tests.Create(ctx, actor, networktest.Request{Subjects: concurrentSubjects, CoreBuildID: builds[0].ID, Type: "connectivity", TestTargetID: target.ID, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	maximum := 0
	for {
		var active int
		if err = db.pool.QueryRow(ctx, `SELECT count(*) FROM public.jobs WHERE batch_id=$1 AND state IN ('leased','running')`, string(concurrentID)).Scan(&active); err != nil {
			t.Fatal(err)
		}
		maximum = max(maximum, active)
		snap, err := db.queue.Snapshot(ctx, db.scope, concurrentID)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Batch.State.Terminal() {
			if snap.Batch.Verdict != jobs.Pass {
				for _, j := range snap.Batch.Children {
					t.Logf("concurrent state=%s attempt=%d error=%v", j.State, j.Attempt, j.Error)
				}
				t.Fatal("four local probes failed")
			}
			for _, j := range snap.Batch.Children {
				if j.Attempt != 1 {
					t.Fatal("concurrency relied on retries")
				}
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("concurrent batch timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if maximum != 4 {
		t.Fatalf("four local slots not exercised: observed %d", maximum)
	}
	t.Log("PASS: four simultaneous native connectivity tasks on one Runner")
	actor.Key = "m3-cancel-download"
	id, err := tests.Create(ctx, actor, networktest.Request{Subjects: []networktest.Subject{{Kind: ir.KindChain, ID: chain.Metadata.ResourceID}}, CoreBuildID: builds[0].ID, Type: "download_throughput", TestTargetID: downloadTarget.ID, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 20 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	var running jobs.Batch
	for {
		snap, err := db.queue.Snapshot(ctx, db.scope, id)
		if err != nil {
			t.Fatal(err)
		}
		running = *snap.Batch
		events, err := db.queue.Events(ctx, db.scope, running.Children[0].ID, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		ready := false
		for _, e := range events.Events {
			ready = ready || e.Phase == "downloading"
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("download did not start")
		case <-time.After(100 * time.Millisecond):
		}
	}
	start := time.Now()
	if _, err = db.queue.Cancel(ctx, db.scope, id, running.Revision); err != nil {
		t.Fatal(err)
	}
	canceled := waitBatch(id)
	if canceled.State != jobs.Canceled || time.Since(start) > 5*time.Second {
		t.Fatal("native cancellation missed five-second cleanup")
	}
	// Every restart happens after application download bytes actually started.
	// The abrupt supervisor SIGKILL boundary is independently covered by the
	// Linux exec suite; this exercises durable state across service restarts.
	request.Config.Limits.DurationMS = 60000
	actor.Key = "m3-recovery-target"
	recoveryTarget, err := tests.WriteTarget(ctx, actor, "", 0, request)
	if err != nil {
		t.Fatal(err)
	}
	faults := []string{"runner_restart", "api_restart"}
	coordination := os.Getenv("PROXYLOOM_M3_FAULT_CONTROL_DIRECTORY")
	if coordination != "" {
		faults = append(faults, "database_restart")
	}
	for _, fault := range faults {
		before := downloads.Load()
		actor.Key = "m3-" + fault
		faultID, e := tests.Create(ctx, actor, networktest.Request{Subjects: []networktest.Subject{{Kind: ir.KindChain, ID: chain.Metadata.ResourceID}}, CoreBuildID: builds[0].ID, Type: "download_throughput", TestTargetID: recoveryTarget.ID, Limits: runnerprotocol.Limits{DurationMS: 60000, MaxBytes: 20 << 20}})
		if e != nil {
			t.Fatal(e)
		}
		for downloads.Load() == before {
			select {
			case <-ctx.Done():
				t.Fatal("fault download never started")
			case <-time.After(50 * time.Millisecond):
			}
		}
		started := time.Now()
		switch fault {
		case "runner_restart":
			stopRunner()
			<-runnerDone
			runnerDone = nil
			stopRunner, runnerDone = startRunner()
		case "api_restart":
			stopControl()
			<-controlDone
			controlDone = nil
			// Exceed the renewal window. A disconnected Runner must stop itself
			// before the server can reclaim and settle the lost attempt.
			time.Sleep(31 * time.Second)
			control, e = runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{reg}, Jobs: db.queue})
			if e != nil {
				t.Fatal(e)
			}
			listener, e = net.Listen("tcp", address)
			if e != nil {
				t.Fatal(e)
			}
			nextControlContext, nextControlStop := context.WithCancel(ctx)
			defer nextControlStop()
			controlCtx, stopControl = nextControlContext, nextControlStop
			controlDone = make(chan error, 1)
			go func() { controlDone <- control.ServeListener(controlCtx, listener, nil) }()
		case "database_restart":
			if e = os.WriteFile(filepath.Join(coordination, "restart-database"), []byte("restart"), 0600); e != nil {
				t.Fatal(e)
			}
			for {
				if _, e = os.Stat(filepath.Join(coordination, "database-restarted")); e == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("database restart not acknowledged")
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		b := waitBatch(faultID)
		if len(b.Children) != 1 || b.Children[0].Attempt != 1 || downloads.Load() != before+1 {
			t.Fatal("restart duplicated download attempt")
		}
		deadline := time.Now().Add(5 * time.Second)
		for activeDownloads.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if activeDownloads.Load() != 0 {
			t.Fatal("terminal task retained an active download")
		}
		t.Logf("PASS: %s during download; terminal=%s, attempts=1, requests=1, active=0, recovery_ms=%d", fault, b.State, time.Since(started).Milliseconds())
	}
	stopFirst()
	actor.Key = "m3-broken-chain"
	id, err = tests.Create(ctx, actor, networktest.Request{Subjects: []networktest.Subject{{Kind: ir.KindChain, ID: chain.Metadata.ResourceID}}, CoreBuildID: builds[0].ID, Type: "connectivity", TestTargetID: target.ID, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if b := waitBatch(id); b.Verdict != jobs.Fail || b.Children[0].Attempt != 1 {
		t.Fatal("node failure bypassed or retried")
	}
	results, _, err := tests.Results(ctx, db.scope, networktest.ResultFilter{Limit: 50})
	if err != nil || len(results) != 12+len(faults) {
		t.Fatal("native result history incomplete", err)
	}
	for _, item := range results {
		if err = apicontract.ValidateDTO("TestResult", item); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("PASS: native cancellation, no node-failure retry, immutable history and byte settlement")
}

func serveM3Browser(t *testing.T, ctx context.Context, db runnerDatabase, pub *storage.Subscriptions, tests *storage.NetworkTests, seed map[string]any) {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("PROXYLOOM_M3_PASSWORD_FILE"))
	if err != nil {
		t.Fatal("browser password file unavailable")
	}
	defer clear(password)
	setup := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	identities, err := storage.NewIdentity(db.pool, identity.Options{ScopeID: db.scope, SetupToken: []byte(setup), TokenPepper: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identities.Setup(ctx, setup, "m3-admin", strings.TrimSpace(string(password)), identity.Source{IP: "127.0.0.1", RequestID: "m3-browser-setup"}); err != nil {
		t.Fatal("browser setup failed")
	}
	mac, _ := apicontract.NewCursorHMAC(bytes.Repeat([]byte{0x55}, 32))
	cursor, _ := apicontract.NewCursorCodec(mac)
	operations, err := storage.NewOperations(db.catalog, db.queue)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.NewHandler("/web", server.Dependencies{Database: db.pool.Ping, Secrets: func() error { return nil }, Identity: identities, PublicURL: os.Getenv("PROXYLOOM_M3_PUBLIC_URL"), Development: true, Nodes: &server.NodeDependencies{Repository: db.catalog, Cursor: cursor}, Jobs: db.queue, JobCursor: cursor, Subscriptions: pub, Tests: tests, Operations: operations}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	listener, err := net.Listen("tcp", "0.0.0.0:18080")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(cleanup)
		<-done
	}()
	data, _ := json.Marshal(seed)
	if err = os.WriteFile("/control/ready", data, 0600); err != nil {
		t.Fatal("browser readiness write failed")
	}
	for {
		if _, err = os.Stat("/control/stop"); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("M3 browser suite timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
