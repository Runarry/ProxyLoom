package storage

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/server"
)

func TestPostgresJobsHTTPAuthorizationCursorCancelAndSSE(t *testing.T) {
	env, queue := postgresJobs(t)
	service, token, _ := newIdentityTest(t, env)
	session := setupIdentityTest(t, env, service, token)
	cursor, err := apicontract.NewCursorCodec(queue)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>synthetic</title>"), 0600) != nil {
		t.Fatal("web fixture failed")
	}
	var logs bytes.Buffer
	handler, err := server.NewHandler(directory, server.Dependencies{Database: env.runtime.Ping, Secrets: func() error { return nil }, Identity: service, PublicURL: "http://localhost:5173", Development: true, Jobs: queue, JobCursor: cursor}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal("job HTTP handler initialization failed", err)
	}
	defer handler.Close()
	host := httptest.NewServer(handler)
	defer host.Close()
	request := func(method, path, body string, headers map[string]string, authenticated bool) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(env.ctx, method, host.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal("HTTP request construction failed")
		}
		if authenticated {
			req.AddCookie(&http.Cookie{Name: server.SessionCookieName, Value: session.ID})
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("X-CSRF-Token", session.CSRFToken)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal("HTTP request failed")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal("HTTP response read failed")
		}
		return response, data
	}
	for range 3 {
		enqueueParse(t, env, queue)
	}
	response, _ := request(http.MethodGet, "/api/v1/jobs", "", nil, false)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("job list accepted anonymous request")
	}
	response, _ = request(http.MethodGet, "/api/v1/jobs", "", map[string]string{"Authorization": "Bearer " + string(newJobID())}, true)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("job list accepted foreign credential")
	}
	response, data := request(http.MethodGet, "/api/v1/jobs?limit=1", "", nil, true)
	if response.StatusCode != http.StatusOK || apicontract.Decode(data, "JobListResponse", new(any)) != nil {
		t.Fatal("job list violated HTTP contract", response.StatusCode)
	}
	var list struct {
		Data []apicontract.Job    `json:"data"`
		Page apicontract.PageInfo `json:"page"`
	}
	if json.Unmarshal(data, &list) != nil || len(list.Data) != 1 || list.Page.NextCursor == "" {
		t.Fatal("job list missing bounded cursor")
	}
	if strings.Contains(string(data), "synthetic-private-job-input") {
		t.Fatal("job list exposed payload")
	}
	response, _ = request(http.MethodGet, "/api/v1/jobs?executor=runner&cursor="+url.QueryEscape(list.Page.NextCursor), "", nil, true)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("job cursor crossed filter binding")
	}
	response, _ = request(http.MethodGet, "/api/v1/jobs?type=connectivity", "", nil, true)
	if response.StatusCode != http.StatusOK {
		t.Fatal("documented future type filter rejected")
	}
	path := "/api/v1/jobs/" + string(list.Data[0].JobID)
	response, data = request(http.MethodGet, path, "", nil, true)
	if response.StatusCode != http.StatusOK || apicontract.Decode(data, "JobResponse", new(any)) != nil {
		t.Fatal("job snapshot violated contract")
	}
	tag := response.Header.Get("ETag")
	response, _ = request(http.MethodPost, path+"/cancel", `{"reason":"synthetic cancellation"}`, nil, true)
	if response.StatusCode != http.StatusPreconditionRequired {
		t.Fatal("job cancellation skipped precondition")
	}
	response, _ = request(http.MethodPost, path+"/cancel", `{"reason":"synthetic cancellation"}`, map[string]string{"If-Match": tag, "X-CSRF-Token": ""}, true)
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("job cancellation skipped CSRF")
	}
	response, data = request(http.MethodPost, path+"/cancel", `{"reason":"synthetic cancellation"}`, map[string]string{"If-Match": tag}, true)
	if response.StatusCode != http.StatusAccepted || apicontract.Decode(data, "JobResponse", new(any)) != nil {
		t.Fatal("job cancel snapshot violated contract", response.StatusCode)
	}
	var canceled apicontract.JobResponse
	if json.Unmarshal(data, &canceled) != nil || !canceled.Data.CancelRequested || canceled.Data.State != apicontract.Canceled {
		t.Fatal("queued cancellation was not persistent")
	}
	response, data = request(http.MethodGet, path+"/events", "", nil, true)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || !strings.Contains(string(data), "event: job_event") || !strings.Contains(string(data), `"phase":"completed"`) {
		t.Fatal("terminal SSE did not replay persistent events", response.StatusCode)
	}
	response, _ = request(http.MethodGet, path+"/events", "", map[string]string{"Last-Event-ID": "00"}, true)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("malformed SSE cursor accepted")
	}
	response, _ = request(http.MethodGet, path+"/events", "", map[string]string{"Last-Event-ID": "99999999"}, true)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("future SSE cursor accepted")
	}
	if strings.Contains(logs.String(), "synthetic-private-job-input") || strings.Contains(logs.String(), session.ID) {
		t.Fatal("job logging exposed secrets")
	}
	// An already streaming request must lose authorization after logout, too.
	active := enqueueParse(t, env, queue)
	req, _ := http.NewRequestWithContext(env.ctx, http.MethodGet, host.URL+"/api/v1/jobs/"+string(active.ID)+"/events", nil)
	req.AddCookie(&http.Cookie{Name: server.SessionCookieName, Value: session.ID})
	stream, err := host.Client().Do(req)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatal("active SSE connection failed")
	}
	before := time.Now()
	if err = service.Logout(env.ctx, session.ID); err != nil {
		t.Fatal("session revocation failed")
	}
	_, err = io.Copy(io.Discard, stream.Body)
	stream.Body.Close()
	if err != nil || time.Since(before) > 4*time.Second {
		t.Fatal("SSE continued after session revocation")
	}
	response, _ = request(http.MethodGet, path, "", nil, true)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("job snapshot accepted revoked session")
	}
}

func TestPostgresJobsHTTPDatabaseFailureDeniesSnapshot(t *testing.T) {
	env, queue := postgresJobs(t)
	service, token, _ := newIdentityTest(t, env)
	session := setupIdentityTest(t, env, service, token)
	job := enqueueParse(t, env, queue)
	cursor, _ := apicontract.NewCursorCodec(queue)
	directory := t.TempDir()
	if os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html>"), 0600) != nil {
		t.Fatal("web fixture failed")
	}
	handler, err := server.NewHandler(directory, server.Dependencies{Database: env.runtime.Ping, Secrets: func() error { return nil }, Identity: service, PublicURL: "http://localhost:5173", Development: true, Jobs: queue, JobCursor: cursor}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	env.runtime.Close()
	request := httptest.NewRequest(http.MethodGet, "http://localhost:5173/api/v1/jobs/"+string(job.ID), nil)
	request.AddCookie(&http.Cookie{Name: server.SessionCookieName, Value: session.ID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "synthetic-private-job-input") {
		t.Fatal("database loss did not fail closed")
	}
}
