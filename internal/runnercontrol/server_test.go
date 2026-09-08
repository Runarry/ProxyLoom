package runnercontrol_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

const runnerID ir.ID = "73000000-0000-4000-8000-000000000001"
const jobID ir.ID = "73000000-0000-4000-8000-000000000002"

func TestMain(m *testing.M) {
	if handled, code := coreexec.SandboxEntrypoint(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

type testPKI struct {
	server, client, unregistered *tls.Config
	fingerprint                  string
}

func makePKI(t *testing.T) testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ProxyLoom synthetic test CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	issue := func(serial int64, server bool) tls.Certificate {
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "synthetic identity"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if server {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			template.DNSNames = []string{"localhost"}
			template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		cert, err := x509.CreateCertificate(rand.Reader, template, root, &private.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{cert}, PrivateKey: private}
	}
	client := issue(3, false)
	digest := sha256.Sum256(client.Certificate[0])
	return testPKI{
		server:       &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, Certificates: []tls.Certificate{issue(2, true)}},
		client:       &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, Certificates: []tls.Certificate{client}},
		unregistered: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, Certificates: []tls.Certificate{issue(4, false)}},
		fingerprint:  hex.EncodeToString(digest[:]),
	}
}

func pinnedBuilds(t *testing.T) []capability.Build {
	t.Helper()
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	var builds []capability.Build
	for _, build := range catalog.Builds() {
		if build.OS == "linux" && build.Arch == "amd64" {
			builds = append(builds, build)
		}
	}
	if len(builds) != 3 {
		t.Fatal("three locked amd64 builds required")
	}
	return builds
}

func registration(pki testPKI, builds []capability.Build) runnercontrol.Registration {
	reg := runnercontrol.Registration{RunnerID: runnerID, CertificateSHA256: pki.fingerprint, Architecture: "amd64", ValidationSlots: 1}
	for _, build := range builds {
		reg.CoreBuildIDs = append(reg.CoreBuildIDs, build.ID)
	}
	return reg
}

func makePayload(build capability.Build, data []byte) runnerprotocol.FrozenPayload {
	formats := map[ir.CoreFamily]ir.OutputFormat{ir.Xray: ir.XrayJSON, ir.SingBox: ir.SingBoxJSON, ir.Mihomo: ir.MihomoYAML}
	return runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: "config_validate", Core: runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version, BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: build.AdapterVersion}, Artifact: runnerprotocol.Artifact{ArtifactID: "73000000-0000-4000-8000-000000000003", Format: formats[build.Family], SHA256: runnerprotocol.Digest(data), ByteLength: int64(len(data)), ContentBase64: base64.StdEncoding.EncodeToString(data)}, Limits: runnerprotocol.Limits{DurationMS: 15000}, ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "none", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32}}
}

type fakeQueue struct {
	lease     *jobs.Lease
	claim     jobs.ClaimInput
	completed bool
}

func (q *fakeQueue) Claim(_ context.Context, input jobs.ClaimInput) (*jobs.Lease, error) {
	q.claim = input
	return q.lease, nil
}
func (q *fakeQueue) Heartbeat(_ context.Context, id jobs.LeaseIdentity) (jobs.Heartbeat, error) {
	if q.lease == nil || id != q.lease.Identity || q.completed {
		return jobs.Heartbeat{}, jobs.ErrLeaseLost
	}
	return jobs.Heartbeat{ExpiresAt: time.Now().Add(30 * time.Second)}, nil
}
func (q *fakeQueue) Event(_ context.Context, id jobs.LeaseIdentity, in jobs.EventInput) (jobs.EventReceipt, error) {
	if q.lease == nil || id != q.lease.Identity || q.completed {
		return jobs.EventReceipt{}, jobs.ErrLeaseLost
	}
	return jobs.EventReceipt{JobID: id.JobID, Attempt: id.Attempt, LeaseSeq: runnerprotocol.Sequence(id.LeaseSeq), EventID: in.EventID, Seq: 1}, nil
}
func (q *fakeQueue) Complete(_ context.Context, id jobs.LeaseIdentity, in jobs.Result) (jobs.ResultReceipt, error) {
	if q.lease == nil || id != q.lease.Identity {
		return jobs.ResultReceipt{}, jobs.ErrLeaseLost
	}
	r := jobs.ResultReceipt{JobID: id.JobID, Attempt: id.Attempt, LeaseSeq: runnerprotocol.Sequence(id.LeaseSeq), ResultID: "73000000-0000-4000-8000-000000000004", ResultHash: in.Hash, Replayed: q.completed}
	q.completed = true
	return r, nil
}

func post(t *testing.T, client *http.Client, endpoint string, value any, out any) int {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal("mTLS request failed")
	}
	defer response.Body.Close()
	rawResponse, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal("cannot read mTLS response")
	}
	schema := "ErrorResponse"
	if response.StatusCode == http.StatusOK {
		switch {
		case strings.HasSuffix(endpoint, "/runners/heartbeat"):
			schema = "RunnerHeartbeatResponse"
		case strings.HasSuffix(endpoint, "/jobs/lease"):
			schema = "RunnerLeaseResponse"
		case strings.HasSuffix(endpoint, "/heartbeat"):
			schema = "RunnerJobHeartbeatResponse"
		case strings.HasSuffix(endpoint, "/events"):
			schema = "RunnerJobEventResponse"
		case strings.HasSuffix(endpoint, "/result"):
			schema = "RunnerJobResultResponse"
		}
	}
	if _, err := apicontract.CanonicalRequest(rawResponse, schema); err != nil {
		t.Fatalf("mTLS response violates %s contract: %v", schema, err)
	}
	if out != nil {
		if err := json.Unmarshal(rawResponse, out); err != nil {
			t.Fatal("invalid runner response")
		}
	}
	return response.StatusCode
}

func TestMTLSRegistrationFrozenLeaseAndFencing(t *testing.T) {
	pki := makePKI(t)
	builds := pinnedBuilds(t)
	payload := makePayload(builds[0], []byte(`{"synthetic_fixture":true}`))
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{lease: &jobs.Lease{Job: jobs.Job{ID: jobID, Executor: jobs.Runner, Type: jobs.ConfigValidate, CoreBuildID: builds[0].ID}, Identity: jobs.LeaseIdentity{JobID: jobID, WorkerID: runnerID, Attempt: 1, LeaseSeq: 1}, Payload: raw, ExpiresAt: time.Now().Add(30 * time.Second)}}
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{registration(pki, builds)}, Jobs: queue})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(control)
	server.TLS = pki.server
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: pki.client}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	request := runnerprotocol.RegisterRequest{RunnerID: runnerID, Platform: "linux", Architecture: "amd64", AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}, Builds: []runnerprotocol.BuildReport{{CoreBuildID: builds[0].ID, BuildSHA256: builds[0].BinarySHA256}}}
	var registered runnerprotocol.Response[runnerprotocol.RegisterData]
	if post(t, client, server.URL+"/internal/v1/runners/heartbeat", request, &registered) != 200 || registered.Data.RunnerID != runnerID {
		t.Fatal("registered certificate not accepted")
	}
	var leased runnerprotocol.Response[runnerprotocol.LeaseData]
	if post(t, client, server.URL+"/internal/v1/jobs/lease", runnerprotocol.LeaseRequest{RunnerID: runnerID, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}}, &leased) != 200 || leased.Data.Lease == nil {
		t.Fatal("allowed lease missing")
	}
	got := leased.Data.Lease
	if queue.claim.Executor != jobs.Runner || queue.claim.WorkerID != runnerID || len(queue.claim.Types) != 1 || queue.claim.Types[0] != jobs.ConfigValidate || len(queue.claim.CoreBuildIDs) != 1 || queue.claim.CoreBuildIDs[0] != builds[0].ID {
		t.Fatal("claim predicate trusted caller instead of registration")
	}
	hash, _ := runnerprotocol.PayloadHash(got.FrozenPayload)
	if hash != got.PayloadSHA256 || got.Artifact.ContentBase64 != payload.Artifact.ContentBase64 {
		t.Fatal("frozen bytes or hash changed in transport")
	}
	stale := runnerprotocol.HeartbeatRequest{JobID: jobID, Attempt: 1, LeaseSeq: 2}
	if post(t, client, server.URL+"/internal/v1/jobs/"+string(jobID)+"/heartbeat", stale, nil) != 409 {
		t.Fatal("stale fence accepted")
	}
	result := runnerprotocol.ResultRequest{JobID: jobID, Attempt: 1, LeaseSeq: 1, State: "succeeded", Verdict: "pass"}
	result.ResultHash, _ = runnerprotocol.ResultHash(result)
	var receipt runnerprotocol.Response[runnerprotocol.ResultReceipt]
	for i := 0; i < 2; i++ {
		if post(t, client, server.URL+"/internal/v1/jobs/"+string(jobID)+"/result", result, &receipt) != 200 || receipt.Data.Replayed != (i == 1) || receipt.Data.SettledBytes != 0 {
			t.Fatal("same result did not replay safely")
		}
	}
	request.Builds[0].BuildSHA256 = runnerprotocol.Digest([]byte("wrong binary"))
	if post(t, client, server.URL+"/internal/v1/runners/heartbeat", request, nil) != 403 {
		t.Fatal("unverified build digest accepted")
	}
	request.Builds[0].BuildSHA256 = builds[0].BinarySHA256
	request.AvailableSlots.ConfigValidate = 2
	if post(t, client, server.URL+"/internal/v1/runners/heartbeat", request, nil) != 400 {
		t.Fatal("unregistered capacity accepted")
	}
	if post(t, client, server.URL+"/internal/v1/jobs/lease", map[string]any{"runner_id": runnerID, "available_slots": runnerprotocol.Slots{ConfigValidate: 1}, "config": "EXAMPLE_NATIVE_UPLOAD"}, nil) != 400 {
		t.Fatal("native payload upload accepted")
	}
	if post(t, client, server.URL+"/api/v1/jobs", struct{}{}, nil) != 404 {
		t.Fatal("public route exposed on runner listener")
	}
}

func TestMTLSRejectsMissingUnregisteredAndWrongCACertificates(t *testing.T) {
	pki := makePKI(t)
	builds := pinnedBuilds(t)
	control, err := runnercontrol.New(runnercontrol.Config{TLS: pki.server, Registrations: []runnercontrol.Registration{registration(pki, builds)}, Jobs: &fakeQueue{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(control)
	server.TLS = pki.server
	server.StartTLS()
	defer server.Close()
	request := runnerprotocol.LeaseRequest{RunnerID: runnerID, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}}
	unregistered := &http.Client{Transport: &http.Transport{TLSClientConfig: pki.unregistered}, Timeout: 3 * time.Second}
	defer unregistered.CloseIdleConnections()
	if post(t, unregistered, server.URL+"/internal/v1/jobs/lease", request, nil) != 403 {
		t.Fatal("CA-signed but unregistered certificate accepted")
	}
	for _, test := range []struct {
		name   string
		config *tls.Config
	}{{"missing", &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pki.client.RootCAs}}, {"wrong_ca", makePKI(t).client}} {
		t.Run(test.name, func(t *testing.T) {
			conf := test.config.Clone()
			conf.RootCAs = pki.client.RootCAs
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: conf}, Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			response, err := client.Post(server.URL+"/internal/v1/jobs/lease", "application/json", bytes.NewReader([]byte(`{}`)))
			if err == nil {
				response.Body.Close()
				t.Fatal("untrusted client completed TLS")
			}
		})
	}
	plain := httptest.NewRecorder()
	control.ServeHTTP(plain, httptest.NewRequest(http.MethodPost, "/internal/v1/jobs/lease", bytes.NewReader([]byte(`{}`))))
	if plain.Code != http.StatusForbidden {
		t.Fatal("handler trusted plaintext transport")
	}
}
