package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

const importHTTPBatch ir.ID = "41000000-0000-4000-8000-000000000001"

type fakeImports struct {
	created       int
	committed     int
	got           int
	scope, actor  ir.ID
	format        string
	size          int
	decisionCount int
	expected      int64
	key           string
}

func (f *fakeImports) Create(_ context.Context, input imports.CreateInput) (imports.Accepted, error) {
	f.created++
	f.scope = input.ScopeID
	f.actor = input.PrincipalID
	f.format = input.InputFormat
	f.size = len(input.Text)
	f.key = input.IdempotencyKey
	if input.InputFormat != "auto" && input.InputFormat != "uri_list" && input.InputFormat != "base64_uri_list" {
		return imports.Accepted{}, imports.ErrUnsupported
	}
	return imports.Accepted{BatchID: importHTTPBatch, JobID: "42000000-0000-4000-8000-000000000001", Revision: 1, State: "queued"}, nil
}
func (f *fakeImports) Get(_ context.Context, scope, id ir.ID, options imports.PageOptions) (imports.Batch, error) {
	f.got++
	f.scope = scope
	return imports.Batch{BatchID: id, Revision: 2, JobID: "42000000-0000-4000-8000-000000000001", State: "ready", Candidates: []imports.Candidate{}, Diagnostics: ir.Diagnostics{}, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(imports.Retention)}, nil
}
func (f *fakeImports) Commit(_ context.Context, input imports.CommitInput) (imports.Commit, error) {
	f.committed++
	f.scope = input.ScopeID
	f.actor = input.PrincipalID
	f.decisionCount = len(input.Decisions)
	f.expected = input.ExpectedRevision
	f.key = input.IdempotencyKey
	items := make([]imports.CommitItem, 0, len(input.Decisions))
	for _, d := range input.Decisions {
		items = append(items, imports.CommitItem{CandidateID: d.CandidateID, Status: "skipped"})
	}
	return imports.Commit{BatchID: input.BatchID, Revision: 3, Items: items}, nil
}

func importHTTP(t *testing.T) (http.Handler, *fakeImports, identity.Session) {
	t.Helper()
	service := newFakeIdentity()
	session := service.session(false)
	auth, err := NewAuthentication(service, AuthenticationConfig{PublicURL: testAuthOrigin})
	if err != nil {
		t.Fatal("import authentication unavailable")
	}
	repo := &fakeImports{}
	router := gin.New()
	mountImports(router, auth, repo)
	return apicontract.RequestIDs(router), repo, session
}
func importRequest(handler http.Handler, session identity.Session, method, path, content string, body []byte, etag string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", content)
	req.Header.Set("Origin", testAuthOrigin)
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	req.Header.Set("Idempotency-Key", "import-test-key")
	if session.ID != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestImportsHTTPAuthenticatedStrictPreviewBoundary(t *testing.T) {
	handler, repo, session := importHTTP(t)
	uri := url.URL{Scheme: "trojan", User: url.User("EXAMPLE_IMPORT_PASSWORD"), Host: "node.example.invalid:443", Fragment: "Name"}
	body, _ := json.Marshal(map[string]string{"text": uri.String(), "format": "uri_list"})
	response := importRequest(handler, session, http.MethodPost, "/api/v1/imports", "application/json", body, "")
	if response.Code != 202 || repo.created != 1 || repo.scope != session.User.ScopeID || repo.actor != session.User.ID || repo.key != "import-test-key" || response.Header().Get("ETag") != `"r1"` {
		t.Fatal("authenticated preview creation failed")
	}
	if strings.Contains(response.Body.String(), "EXAMPLE_IMPORT_PASSWORD") {
		t.Fatal("creation response leaked raw input")
	}
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"duplicate field", `{"text":"first","text":"second","format":"auto"}`, 400},
		{"unknown scope", `{"text":"x","format":"auto","scope_id":"10000000-0000-4000-8000-000000000002"}`, 400},
		{"unsupported source", `{"text":"x","format":"auto","source_id":"10000000-0000-4000-8000-000000000002"}`, 422},
		{"native format", `{"text":"x","format":"xray_json"}`, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := importRequest(handler, session, http.MethodPost, "/api/v1/imports", "application/json", []byte(test.body), "")
			if r.Code != test.status {
				t.Fatalf("boundary status = %d", r.Code)
			}
		})
	}
	before := repo.created
	response = importRequest(handler, identity.Session{}, http.MethodPost, "/api/v1/imports", "application/json", body, "")
	if response.Code != 401 || repo.created != before {
		t.Fatal("unauthenticated import reached repository")
	}
	invalid := append([]byte(`{"text":"`), 0xff)
	invalid = append(invalid, []byte(`","format":"auto"}`)...)
	if response = importRequest(handler, session, http.MethodPost, "/api/v1/imports", "application/json", invalid, ""); response.Code != 400 {
		t.Fatal("invalid UTF-8 accepted")
	}
	if response = importRequest(handler, session, http.MethodGet, "/api/v1/imports/"+string(importHTTPBatch)+"?limit=201", "", nil, ""); response.Code != 400 {
		t.Fatal("unbounded candidate page accepted")
	}
}

func TestImportsHTTPBoundedMultipartAndUntrustedFilename(t *testing.T) {
	handler, repo, session := importHTTP(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "../../EXAMPLE_UNTRUSTED_FILENAME.txt")
	if err != nil {
		t.Fatal("multipart fixture failed")
	}
	_, _ = file.Write([]byte("socks://node.example.invalid:1080#Local"))
	_ = writer.WriteField("format", "uri_list")
	_ = writer.Close()
	response := importRequest(handler, session, http.MethodPost, "/api/v1/imports", writer.FormDataContentType(), body.Bytes(), "")
	if response.Code != 202 || repo.created != 1 || repo.format != "uri_list" || repo.size == 0 {
		t.Fatal("bounded multipart rejected")
	}
	if strings.Contains(response.Body.String(), "EXAMPLE_UNTRUSTED_FILENAME") {
		t.Fatal("filename echoed")
	}
	body.Reset()
	writer = multipart.NewWriter(&body)
	file, _ = writer.CreateFormFile("file", "binary.txt")
	_, _ = file.Write([]byte{0xff})
	_ = writer.WriteField("format", "auto")
	_ = writer.Close()
	if response = importRequest(handler, session, http.MethodPost, "/api/v1/imports", writer.FormDataContentType(), body.Bytes(), ""); response.Code != 400 {
		t.Fatal("binary import accepted")
	}
	body.Reset()
	writer = multipart.NewWriter(&body)
	file, _ = writer.CreateFormFile("file", "oversized.txt")
	_, _ = file.Write(bytes.Repeat([]byte("x"), imports.MaxInputBytes+1))
	_ = writer.WriteField("format", "auto")
	_ = writer.Close()
	if response = importRequest(handler, session, http.MethodPost, "/api/v1/imports", writer.FormDataContentType(), body.Bytes(), ""); response.Code != 413 {
		t.Fatal("oversized file accepted")
	}
}

func TestImportsHTTP5000DecisionCommitAndPreconditions(t *testing.T) {
	handler, repo, session := importHTTP(t)
	path := "/api/v1/imports/" + string(importHTTPBatch) + "/commit"
	decisions := make([]imports.Decision, 5000)
	for i := range decisions {
		decisions[i] = imports.Decision{CandidateID: ir.ID(fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1)), Action: "skip"}
	}
	body, err := json.Marshal(struct {
		Decisions []imports.Decision `json:"decisions"`
	}{decisions})
	if err != nil {
		t.Fatal("selection fixture failed")
	}
	if response := importRequest(handler, session, http.MethodPost, path, "application/json", body, ""); response.Code != 428 || repo.committed != 0 {
		t.Fatal("missing If-Match accepted")
	}
	response := importRequest(handler, session, http.MethodPost, path, "application/json", body, `"r2"`)
	if response.Code != 200 || repo.committed != 1 || repo.decisionCount != 5000 || repo.expected != 2 || repo.actor != session.User.ID || repo.key != "import-test-key" {
		t.Fatalf("large confirmation failed with status %d", response.Code)
	}
	for _, body := range []string{
		`{"decisions":[{"candidate_id":"43000000-0000-4000-8000-000000000001","action":"bind","resource_id":"44000000-0000-4000-8000-000000000001","expected_revision":"1"}]}`,
		`{"decisions":[{"candidate_id":"43000000-0000-4000-8000-000000000001","action":"skip"}],"source_revision":"1"}`,
	} {
		if response := importRequest(handler, session, http.MethodPost, path, "application/json", []byte(body), `"r2"`); response.Code != 422 {
			t.Fatal("source-dependent operation accepted")
		}
	}
}
