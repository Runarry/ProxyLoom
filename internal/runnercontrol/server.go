// Package runnercontrol serves only the separate, certificate-authenticated
// runner listener. It has no executor and delegates all state to the fenced queue.
package runnercontrol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var ErrConfiguration = errors.New("runner_listener_invalid_configuration")
var ErrServe = errors.New("runner_listener_failed")

type Queue interface {
	Claim(context.Context, jobs.ClaimInput) (*jobs.Lease, error)
	Heartbeat(context.Context, jobs.LeaseIdentity) (jobs.Heartbeat, error)
	Event(context.Context, jobs.LeaseIdentity, jobs.EventInput) (jobs.EventReceipt, error)
	Complete(context.Context, jobs.LeaseIdentity, jobs.Result) (jobs.ResultReceipt, error)
}

type Registration struct {
	RunnerID          ir.ID   `json:"runner_id"`
	CertificateSHA256 string  `json:"certificate_sha256"`
	Architecture      string  `json:"arch"`
	CoreBuildIDs      []ir.ID `json:"core_build_ids"`
	ValidationSlots   int32   `json:"validation_slots"`
}

type Config struct {
	TLS           *tls.Config
	Registrations []Registration
	Catalog       *capability.Catalog
	Jobs          Queue
}

type registration struct {
	Registration
	builds map[ir.ID]capability.Build
}
type session struct {
	builds []ir.ID
	seen   time.Time
}
type Server struct {
	tls        *tls.Config
	queue      Queue
	registered map[string]registration
	mu         sync.Mutex
	sessions   map[ir.ID]session
}

func ReadRegistry(path string) ([]Registration, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrConfiguration
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return nil, ErrConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return nil, ErrConfiguration
	}
	var registrations []Registration
	if decode(data, &registrations) != nil || len(registrations) < 1 || len(registrations) > 128 {
		return nil, ErrConfiguration
	}
	return registrations, nil
}

func New(config Config) (*Server, error) {
	if config.Jobs == nil || config.TLS == nil || config.TLS.ClientAuth != tls.RequireAndVerifyClientCert || config.TLS.ClientCAs == nil || len(config.TLS.Certificates) != 1 || len(config.Registrations) < 1 || len(config.Registrations) > 128 {
		return nil, ErrConfiguration
	}
	if config.Catalog == nil {
		var err error
		config.Catalog, err = capability.Load()
		if err != nil {
			return nil, ErrConfiguration
		}
	}
	s := &Server{tls: config.TLS.Clone(), queue: config.Jobs, registered: make(map[string]registration), sessions: make(map[ir.ID]session)}
	s.tls.MinVersion = tls.VersionTLS13
	seenIDs := make(map[ir.ID]bool)
	for _, r := range config.Registrations {
		if r.RunnerID.Validate() != nil || !runnerprotocol.ValidDigest(r.CertificateSHA256) || seenIDs[r.RunnerID] || r.Architecture != "amd64" && r.Architecture != "arm64" || r.ValidationSlots != 1 || len(r.CoreBuildIDs) < 1 || len(r.CoreBuildIDs) > 100 {
			return nil, ErrConfiguration
		}
		if _, duplicate := s.registered[r.CertificateSHA256]; duplicate {
			return nil, ErrConfiguration
		}
		seenIDs[r.RunnerID] = true
		reg := registration{Registration: r, builds: make(map[ir.ID]capability.Build)}
		reg.CoreBuildIDs = append([]ir.ID(nil), r.CoreBuildIDs...)
		for _, id := range r.CoreBuildIDs {
			build, err := config.Catalog.Build(id)
			if err != nil || build.OS != "linux" || build.Arch != r.Architecture {
				return nil, ErrConfiguration
			}
			if _, duplicate := reg.builds[id]; duplicate {
				return nil, ErrConfiguration
			}
			reg.builds[id] = build
		}
		s.registered[r.CertificateSHA256] = reg
	}
	return s, nil
}

func (s *Server) Serve(ctx context.Context, addr string, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrServe
	}
	return s.ServeListener(ctx, listener, logger)
}

// ServeListener is also used by integration tests with an ephemeral real TCP
// socket; TLS is always applied here, never optional based on deployment mode.
func (s *Server) ServeListener(ctx context.Context, listener net.Listener, logger *slog.Logger) error {
	server := &http.Server{Handler: s, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(tls.NewListener(listener, s.tls)) }()
	if logger != nil {
		logger.Info("runner_listener_started")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return ErrServe
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			<-finished
			return ErrServe
		}
		<-finished
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	requestID := newID()
	reg, ok := s.authenticate(r)
	if !ok {
		fail(w, requestID, http.StatusForbidden, "RUNNER_IDENTITY_REJECTED")
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		fail(w, requestID, http.StatusNotFound, "NOT_FOUND")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, requestID, http.StatusUnsupportedMediaType, "INVALID_REQUEST")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	defer clear(data)
	if err != nil {
		fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if schema := requestSchema(r.URL.Path); schema != "" {
		canonical, err := apicontract.CanonicalRequest(data, schema)
		clear(canonical)
		if err != nil {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
	}
	switch r.URL.Path {
	case "/internal/v1/runners/heartbeat":
		var request runnerprotocol.RegisterRequest
		if decode(data, &request) != nil || request.RunnerID != reg.RunnerID || request.Platform != "linux" || request.Architecture != reg.Architecture || !validSlots(request.AvailableSlots, reg.ValidationSlots) || request.Load.ActiveJobs < 0 || request.Load.ActiveJobs > reg.ValidationSlots || request.Load.MemoryAvailableBytes < 0 || request.Load.MemoryAvailableBytes > 9007199254740991 || len(request.Builds) < 1 || len(request.Builds) > 100 {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		ids := make([]ir.ID, 0, len(request.Builds))
		seen := make(map[ir.ID]bool)
		for _, report := range request.Builds {
			build, allowed := reg.builds[report.CoreBuildID]
			if !allowed || seen[report.CoreBuildID] || build.BinarySHA256 != report.BuildSHA256 {
				fail(w, requestID, http.StatusForbidden, "CORE_BINARY_MISMATCH")
				return
			}
			seen[report.CoreBuildID] = true
			ids = append(ids, report.CoreBuildID)
		}
		s.mu.Lock()
		s.sessions[reg.RunnerID] = session{builds: ids, seen: time.Now()}
		s.mu.Unlock()
		respond(w, requestID, runnerprotocol.RegisterData{RunnerID: reg.RunnerID, HeartbeatIntervalMS: 5000, LeaseDurationMS: 30000, ServerTime: time.Now().UTC(), AcceptedBuildIDs: ids})
	case "/internal/v1/jobs/lease":
		var request runnerprotocol.LeaseRequest
		if decode(data, &request) != nil || request.RunnerID != reg.RunnerID || !validSlots(request.AvailableSlots, reg.ValidationSlots) {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		s.mu.Lock()
		active, exists := s.sessions[reg.RunnerID]
		s.mu.Unlock()
		if !exists || time.Since(active.seen) > jobs.LeaseDuration {
			fail(w, requestID, http.StatusForbidden, "RUNNER_REGISTRATION_REQUIRED")
			return
		}
		if request.AvailableSlots.ConfigValidate == 0 {
			respond(w, requestID, runnerprotocol.LeaseData{})
			return
		}
		lease, err := s.queue.Claim(r.Context(), jobs.ClaimInput{Executor: jobs.Runner, WorkerID: reg.RunnerID, Types: []jobs.Type{jobs.ConfigValidate}, CoreBuildIDs: append([]ir.ID(nil), active.builds...)})
		if err != nil {
			queueError(w, requestID, err)
			return
		}
		if lease == nil {
			respond(w, requestID, runnerprotocol.LeaseData{})
			return
		}
		defer clear(lease.Payload)
		var payload runnerprotocol.FrozenPayload
		if decode(lease.Payload, &payload) != nil || runnerprotocol.ValidateConfigPayload(payload) != nil || payload.Core.CoreBuildID != lease.Job.CoreBuildID || lease.Job.Executor != jobs.Runner || lease.Job.Type != jobs.ConfigValidate || lease.Identity.WorkerID != reg.RunnerID || lease.Identity.JobID != lease.Job.ID || !lease.Identity.Valid() || !matchingCore(payload.Core, reg.builds[payload.Core.CoreBuildID]) {
			fail(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
			return
		}
		hash, err := runnerprotocol.PayloadHash(payload)
		if err != nil {
			fail(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
			return
		}
		respond(w, requestID, runnerprotocol.LeaseData{Lease: &runnerprotocol.Lease{JobID: lease.Identity.JobID, Attempt: lease.Identity.Attempt, LeaseSeq: runnerprotocol.Sequence(lease.Identity.LeaseSeq), LeaseExpiresAt: lease.ExpiresAt, FrozenPayload: payload, PayloadSHA256: hash}})
	default:
		s.jobRequest(w, r, requestID, reg, data)
	}
}

func requestSchema(path string) string {
	switch path {
	case "/internal/v1/runners/heartbeat":
		return "RunnerHeartbeatRequest"
	case "/internal/v1/jobs/lease":
		return "RunnerLeaseRequest"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/internal/v1/jobs/"), "/")
	if !strings.HasPrefix(path, "/internal/v1/jobs/") || len(parts) != 2 {
		return ""
	}
	switch parts[1] {
	case "heartbeat":
		return "RunnerJobHeartbeatRequest"
	case "events":
		return "RunnerJobEventRequest"
	case "result":
		return "RunnerJobResultRequest"
	}
	return ""
}

func (s *Server) jobRequest(w http.ResponseWriter, r *http.Request, requestID string, reg registration, data []byte) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/v1/jobs/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/internal/v1/jobs/") || len(parts) != 2 || ir.ID(parts[0]).Validate() != nil {
		fail(w, requestID, http.StatusNotFound, "NOT_FOUND")
		return
	}
	identity := func(id ir.ID, attempt int32, seq runnerprotocol.Sequence) (jobs.LeaseIdentity, bool) {
		i := jobs.LeaseIdentity{JobID: id, WorkerID: reg.RunnerID, Attempt: attempt, LeaseSeq: int64(seq)}
		return i, i.Valid() && id == ir.ID(parts[0])
	}
	switch parts[1] {
	case "heartbeat":
		var request runnerprotocol.HeartbeatRequest
		if decode(data, &request) != nil {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		id, valid := identity(request.JobID, request.Attempt, request.LeaseSeq)
		if !valid {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		heartbeat, err := s.queue.Heartbeat(r.Context(), id)
		if err != nil {
			queueError(w, requestID, err)
			return
		}
		respond(w, requestID, runnerprotocol.HeartbeatData{JobID: id.JobID, Attempt: id.Attempt, LeaseSeq: request.LeaseSeq, LeaseExpiresAt: heartbeat.ExpiresAt, CancelRequested: heartbeat.CancelRequested})
	case "events":
		var request runnerprotocol.EventRequest
		if decode(data, &request) != nil {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		id, valid := identity(request.JobID, request.Attempt, request.LeaseSeq)
		if !valid {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		receipt, err := s.queue.Event(r.Context(), id, jobs.EventInput{EventID: request.EventID, Phase: request.Phase, Completed: request.Completed, Total: request.Total, Verdict: jobs.Verdict(request.Verdict), Error: request.Error})
		if err != nil {
			queueError(w, requestID, err)
			return
		}
		respond(w, requestID, receipt)
	case "result":
		var request runnerprotocol.ResultRequest
		if decode(data, &request) != nil || runnerprotocol.ValidateConfigMetrics(request.Metrics) != nil || !runnerprotocol.ValidDigest(request.ResultHash) {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		id, valid := identity(request.JobID, request.Attempt, request.LeaseSeq)
		if !valid {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		hash, err := runnerprotocol.ResultHash(request)
		if err != nil || hash != request.ResultHash {
			fail(w, requestID, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		receipt, err := s.queue.Complete(r.Context(), id, jobs.Result{State: jobs.State(request.State), Verdict: jobs.Verdict(request.Verdict), Metrics: request.Metrics, Error: request.Error, Hash: request.ResultHash})
		if err != nil {
			queueError(w, requestID, err)
			return
		}
		respond(w, requestID, receipt)
	default:
		fail(w, requestID, http.StatusNotFound, "NOT_FOUND")
	}
}

func matchingCore(core runnerprotocol.CoreIdentity, build capability.Build) bool {
	return core.CoreBuildID == build.ID && core.BuildSHA256 == build.BinarySHA256 && core.CoreFamily == build.Family && core.Version == build.Version && core.Platform == build.OS && core.Architecture == build.Arch && core.AdapterVersion == build.AdapterVersion
}

func validSlots(slots runnerprotocol.Slots, maximum int32) bool {
	return slots.ConfigValidate >= 0 && slots.ConfigValidate <= maximum && slots.Connectivity == 0 && slots.DownloadThroughput == 0
}

func (s *Server) authenticate(r *http.Request) (registration, bool) {
	if r.TLS == nil || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return registration{}, false
	}
	cert := r.TLS.PeerCertificates[0]
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return registration{}, false
	}
	digest := sha256.Sum256(cert.Raw)
	reg, ok := s.registered[hex.EncodeToString(digest[:])]
	return reg, ok
}

func decode(data []byte, out any) error {
	return runnerprotocol.DecodeStrict(data, out)
}

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "runner-request"
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func respond(w http.ResponseWriter, id string, value any) {
	_ = json.NewEncoder(w).Encode(runnerprotocol.Response[any]{RequestID: id, Data: value})
}
func fail(w http.ResponseWriter, id string, status int, code string) {
	// Internal branch labels are translated to the published ErrorCode enum.
	switch code {
	case "RUNNER_IDENTITY_REJECTED":
		code = "PERMISSION_DENIED"
	case "RUNNER_REGISTRATION_REQUIRED":
		code = "RUNNER_UNAVAILABLE"
	case "NOT_FOUND":
		code = "RESOURCE_NOT_FOUND"
	case "INVALID_REQUEST":
		code = "MALFORMED_REQUEST"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		RequestID string                    `json:"request_id"`
		Error     *runnerprotocol.SafeError `json:"error"`
	}{id, &runnerprotocol.SafeError{Code: code, Message: "The runner request could not be accepted.", Details: []runnerprotocol.Detail{}}})
}
func queueError(w http.ResponseWriter, id string, err error) {
	switch {
	case errors.Is(err, jobs.ErrInvalidInput):
		fail(w, id, http.StatusBadRequest, "INVALID_REQUEST")
	case errors.Is(err, jobs.ErrNotFound):
		fail(w, id, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, jobs.ErrLeaseLost), errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrCanceled):
		fail(w, id, http.StatusConflict, "LEASE_LOST")
	default:
		fail(w, id, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
	}
}
