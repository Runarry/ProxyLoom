package runnercontrol_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runner"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/server"
	"github.com/Runarry/ProxyLoom/internal/storage"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPostgresM2RealPublication(t *testing.T) {
	root := os.Getenv("PROXYLOOM_RUNNER_REAL_CORES")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || root == "" || os.Getenv("PROXYLOOM_TEST_DATABASE_DSN_FILE") == "" {
		if os.Getenv("PROXYLOOM_REQUIRE_RUNNER_TESTS") == "true" {
			t.Fatal("real M2 acceptance environment incomplete")
		}
		t.Skip("requires Linux, pinned kernels and isolated PostgreSQL")
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
	if err = db.catalog.EnsureBuiltinClientPresets(ctx, db.scope); err != nil {
		t.Fatal(err)
	}
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{registration(pki, builds)}, Jobs: db.queue})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 4)
	go func() { done <- control.ServeListener(runCtx, listener, nil) }()
	client, err := runner.NewClient(runner.ClientConfig{RunnerID: runnerID, APIURL: "https://" + listener.Addr().String(), TLS: pki.client, CoreRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- client.Run(runCtx) }()
	worker, err := jobs.NewWorker(db.queue, jobs.WorkerConfig{WorkerID: jobs.NewID(), Handlers: map[jobs.Type]jobs.Handler{jobs.Compile: pub.HandleCompile}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- worker.Run(runCtx) }()
	go func() { done <- pub.Run(runCtx) }()
	defer func() {
		stop()
		for range 4 {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Error("M2 background shutdown failed")
			}
		}
	}()
	create := func(name string, payload ir.ResourcePayload) ir.Resource {
		var r ir.Resource
		err := db.catalog.Transact(ctx, db.scope, func(tx catalog.Tx) error {
			var err error
			r, err = tx.Create(ctx, catalog.CreateInput{Name: name, Tags: []string{}, Enabled: true, Payload: payload})
			return err
		})
		if err != nil {
			t.Fatal("M2 seed resource failed:", err)
		}
		return r
	}
	verify, udp := true, false
	node := create("M2 synthetic node", &ir.Node{SchemaVersion: 1, Protocol: ir.Trojan, Endpoint: ir.Endpoint{Host: "192.0.2.10", Port: 443}, Auth: &ir.PasswordAuth{Kind: ir.AuthPassword, Password: "EXAMPLE_ONLY_M2_CREDENTIAL"}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "example.invalid", VerifyCertificate: &verify}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}})
	exitNode := create("M2 synthetic exit", &ir.Node{SchemaVersion: 1, Protocol: ir.Trojan, Endpoint: ir.Endpoint{Host: "192.0.2.11", Port: 443}, Auth: &ir.PasswordAuth{Kind: ir.AuthPassword, Password: "EXAMPLE_ONLY_M2_EXIT"}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "example.invalid", VerifyCertificate: &verify}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}})
	chain := create("M2 two-hop chain", &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: node.Metadata.ResourceID}, {NodeID: exitNode.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed})
	routing := create("M2 routing", &ir.RoutingProfile{SchemaVersion: 1, Rules: []ir.RoutingRule{}, Final: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}, DomainResolutionMode: ir.PreserveDomain})
	dns := create("M2 DNS", &ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}}, Resolvers: []ir.DNSResolver{{ResolverID: "local", Kind: ir.DNSLocal}}, Rules: []ir.DNSRule{}, FinalResolver: "local"})
	presets, err := db.catalog.ListClientPresets(ctx, db.scope, catalog.ClientPresetListOptions{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	profile := ir.SubscriptionProfile{SchemaVersion: 1, Members: ir.SubscriptionMembers{IncludeIDs: []ir.ID{chain.Metadata.ResourceID}, ExcludeIDs: []ir.ID{}, Selector: ir.TagSelector{AllTags: []string{}, AnyTags: []string{}, NoneTags: []string{}}}, RoutingProfileID: routing.Metadata.ResourceID, DNSProfileID: dns.Metadata.ResourceID, Targets: []ir.SubscriptionTarget{}, PublishPolicy: "strict_all_targets"}
	keys := []string{}
	for _, b := range builds {
		for _, p := range presets.Items {
			preset := p.Payload.(*ir.ClientPreset)
			if preset.CoreFamily == b.Family && !preset.ControlAPI.Enabled {
				key := string(b.Family) + "-default"
				keys = append(keys, key)
				profile.Targets = append(profile.Targets, ir.SubscriptionTarget{Key: key, CoreBuildID: b.ID, ClientPresetID: p.Metadata.ResourceID, Format: preset.Format, PolicyOverrides: []ir.PolicyOverride{}})
			}
		}
	}
	resource := create("M2 real publication", &profile)
	actor := subscriptions.Actor{ScopeID: db.scope, ID: jobs.NewID()}
	if os.Getenv("PROXYLOOM_M2_BROWSER_SERVER") == "true" {
		serveM2Browser(t, ctx, db, pub, resource, actor)
		return
	}
	batch, err := pub.Compile(ctx, actor, resource.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: keys})
	if err != nil {
		t.Fatal("M2 freeze failed:", err)
	}
	for batch.State != "ready" {
		if batch.State == "failed" || batch.State == "obsolete" {
			t.Fatalf("M2 real validation state=%s diagnostics=%v", batch.State, batch.Diagnostics)
		}
		select {
		case <-ctx.Done():
			t.Fatal("M2 native timeout")
		case <-time.After(100 * time.Millisecond):
		}
		batch, err = pub.GetBatch(ctx, actor, batch.BatchID)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := subscriptions.PublishRequest{BatchID: batch.BatchID, EffectivePreviewHash: batch.EffectivePreviewHash}
	request.Confirmation.Acknowledged = true
	publication, err := pub.Publish(ctx, actor, resource.Metadata.ResourceID, 1, request)
	if err != nil {
		t.Fatal("M2 real publish failed:", err)
	}
	issued, err := pub.IssueToken(ctx, actor, resource.Metadata.ResourceID, subscriptions.TokenRequest{Name: "native", AllowedTargets: keys})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range publication.Targets {
		download, err := pub.Download(ctx, issued.Token, target.TargetKey)
		if err != nil {
			t.Fatal(err)
		}
		artifacts, err := pub.Export(ctx, actor, publication.PublicationID, []string{target.TargetKey}, target.Format, true)
		if err != nil || len(artifacts) != 1 || artifacts[0].Content != string(download.Bytes) {
			t.Fatal("M2 downloaded bytes differ from validated publication")
		}
		clear(download.Bytes)
		job, err := db.queue.Get(ctx, db.scope, target.ValidationJobID)
		if err != nil || job.State != jobs.Succeeded || job.Verdict != jobs.Pass || job.Attempt != 1 {
			t.Fatal("M2 publication lacked successful exact-build native validation")
		}
	}
	if _, err = pub.RevokeToken(ctx, actor, issued.Metadata.TokenID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = pub.Download(ctx, issued.Token, keys[0]); !errors.Is(err, subscriptions.ErrToken) {
		t.Fatal("M2 revoked real publication remained available")
	}
	for _, cipher := range []ir.VMessCipher{ir.VMessAuto, ir.VMessAES128GCM, ir.VMessChaCha20Poly1305, ir.VMessNone, ir.VMessZero} {
		vmess := create("M2 VMess "+string(cipher), &ir.Node{SchemaVersion: 1, Protocol: ir.VMess, Endpoint: ir.Endpoint{Host: "192.0.2.12", Port: 443}, Auth: &ir.VMessAuth{Kind: ir.AuthVMessAEAD, UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Cipher: cipher}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}})
		variant := profile.Clone()
		variant.Members.IncludeIDs = []ir.ID{vmess.Metadata.ResourceID}
		candidate := create("M2 cipher profile "+string(cipher), &variant)
		checked, err := pub.Compile(ctx, actor, candidate.Metadata.ResourceID, 1, subscriptions.CompileRequest{TargetKeys: keys})
		if err != nil {
			t.Fatal(err)
		}
		for checked.State != "ready" {
			if checked.State == "failed" || checked.State == "obsolete" {
				t.Fatalf("VMess %s validation failed: %v", cipher, checked.Diagnostics)
			}
			select {
			case <-ctx.Done():
				t.Fatal("cipher matrix timeout")
			case <-time.After(100 * time.Millisecond):
			}
			checked, err = pub.GetBatch(ctx, actor, checked.BatchID)
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("PASS: VMess cipher %s, three final configuration checks", cipher)
	}
	t.Log("PASS: frozen profile -> persistent compile -> mTLS three locked kernels -> confirmed atomic publication -> exact-byte downloads -> committed revocation")
}

func serveM2Browser(t *testing.T, ctx context.Context, db runnerDatabase, pub *storage.Subscriptions, profile ir.Resource, actor subscriptions.Actor) {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("PROXYLOOM_M2_PASSWORD_FILE"))
	if err != nil {
		t.Fatal("browser password file unavailable")
	}
	defer clear(password)
	setup := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	identities, err := storage.NewIdentity(db.pool, identity.Options{ScopeID: db.scope, SetupToken: []byte(setup), TokenPepper: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identities.Setup(ctx, setup, "m2-admin", strings.TrimSpace(string(password)), identity.Source{IP: "127.0.0.1", RequestID: "m2-browser-setup"}); err != nil {
		t.Fatal("browser setup failed")
	}
	mac, _ := apicontract.NewCursorHMAC(bytes.Repeat([]byte{0x55}, 32))
	cursor, _ := apicontract.NewCursorCodec(mac)
	handler, err := server.NewHandler("/web", server.Dependencies{Database: db.pool.Ping, Secrets: func() error { return nil }, Identity: identities, PublicURL: os.Getenv("PROXYLOOM_M2_PUBLIC_URL"), Development: true, Nodes: &server.NodeDependencies{Repository: db.catalog, Cursor: cursor}, Jobs: db.queue, JobCursor: cursor, Subscriptions: pub}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("browser handler failed:", err)
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
		_ = httpServer.Shutdown(cleanup)
		<-done
	}()
	if err = os.WriteFile("/control/ready", []byte(profile.Metadata.ResourceID), 0600); err != nil {
		t.Fatal("browser readiness write failed")
	}
	for {
		if _, err := os.Stat("/control/stop"); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("browser suite timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
