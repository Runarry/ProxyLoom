// Package runnerprotocol contains only the bounded wire values shared by the
// API and Runner. It has no database, secret-key or management-HTTP dependency.
package runnerprotocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

var ErrInvalid = errors.New("runner_protocol_invalid")

type Sequence int64

func (n Sequence) MarshalJSON() ([]byte, error) {
	if n < 0 {
		return nil, ErrInvalid
	}
	return json.Marshal(strconv.FormatInt(int64(n), 10))
}
func (n *Sequence) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalid
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != value {
		return ErrInvalid
	}
	*n = Sequence(parsed)
	return nil
}

type CoreIdentity struct {
	CoreBuildID    ir.ID         `json:"core_build_id"`
	CoreFamily     ir.CoreFamily `json:"core_family"`
	Version        string        `json:"version"`
	BuildSHA256    string        `json:"build_sha256"`
	Platform       string        `json:"platform"`
	Architecture   string        `json:"architecture"`
	AdapterVersion string        `json:"adapter_version"`
}
type Artifact struct {
	ArtifactID    ir.ID           `json:"artifact_id"`
	Format        ir.OutputFormat `json:"format"`
	SHA256        string          `json:"sha256"`
	ByteLength    int64           `json:"byte_length"`
	ContentBase64 string          `json:"content_base64"`
}

func (Artifact) String() string       { return "[REDACTED]" }
func (Artifact) GoString() string     { return "[REDACTED]" }
func (Artifact) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

type Limits struct {
	DurationMS int64 `json:"duration_ms"`
	MaxBytes   int64 `json:"max_bytes"`
}
type ExecutionPolicy struct {
	Network               string `json:"network"`
	AllowEnvironmentProxy bool   `json:"allow_environment_proxy"`
	AllowCoreDownloads    bool   `json:"allow_core_downloads"`
	AllowShell            bool   `json:"allow_shell"`
	TerminationGraceMS    int64  `json:"termination_grace_ms"`
	MemoryLimitBytes      int64  `json:"memory_limit_bytes"`
	ProcessLimit          int32  `json:"process_limit"`
}
type FrozenSubject struct {
	Kind          ir.ResourceKind `json:"kind"`
	ID            ir.ID           `json:"id"`
	Revision      Sequence        `json:"revision"`
	SecurityEpoch Sequence        `json:"security_epoch"`
}

// FrozenPayload is the canonical hash preimage. Attempt, lease expiry and
// fencing sequence are intentionally outside this immutable value.
type FrozenPayload struct {
	SchemaVersion      int             `json:"schema_version"`
	Type               string          `json:"type"`
	Core               CoreIdentity    `json:"core"`
	Artifact           Artifact        `json:"artifact"`
	Subject            *FrozenSubject  `json:"subject,omitempty"`
	Limits             Limits          `json:"limits"`
	ExecutionPolicy    ExecutionPolicy `json:"execution_policy"`
	QuotaReservationID ir.ID           `json:"quota_reservation_id,omitempty"`
}

func (FrozenPayload) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (FrozenPayload) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

type Lease struct {
	JobID          ir.ID     `json:"job_id"`
	Attempt        int32     `json:"attempt"`
	LeaseSeq       Sequence  `json:"lease_seq"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	FrozenPayload
	PayloadSHA256 string `json:"payload_sha256"`
}

func (Lease) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Lease) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

type BuildReport struct {
	CoreBuildID ir.ID  `json:"core_build_id"`
	BuildSHA256 string `json:"build_sha256"`
}
type Slots struct {
	ConfigValidate     int32 `json:"config_validate"`
	Connectivity       int32 `json:"connectivity"`
	DownloadThroughput int32 `json:"download_throughput"`
}
type Load struct {
	ActiveJobs           int32 `json:"active_jobs"`
	CPUThrottled         bool  `json:"cpu_throttled"`
	MemoryAvailableBytes int64 `json:"memory_available_bytes"`
}
type RegisterRequest struct {
	RunnerID       ir.ID         `json:"runner_id"`
	Platform       string        `json:"platform"`
	Architecture   string        `json:"architecture"`
	Builds         []BuildReport `json:"builds"`
	AvailableSlots Slots         `json:"available_slots"`
	Load           Load          `json:"load"`
}
type RegisterData struct {
	RunnerID            ir.ID     `json:"runner_id"`
	HeartbeatIntervalMS int64     `json:"heartbeat_interval_ms"`
	LeaseDurationMS     int64     `json:"lease_duration_ms"`
	ServerTime          time.Time `json:"server_time"`
	AcceptedBuildIDs    []ir.ID   `json:"accepted_build_ids"`
}
type LeaseRequest struct {
	RunnerID       ir.ID `json:"runner_id"`
	AvailableSlots Slots `json:"available_slots"`
}
type HeartbeatRequest struct {
	JobID    ir.ID    `json:"job_id"`
	Attempt  int32    `json:"attempt"`
	LeaseSeq Sequence `json:"lease_seq"`
}
type HeartbeatData struct {
	JobID           ir.ID     `json:"job_id"`
	Attempt         int32     `json:"attempt"`
	LeaseSeq        Sequence  `json:"lease_seq"`
	LeaseExpiresAt  time.Time `json:"lease_expires_at"`
	CancelRequested bool      `json:"cancel_requested"`
}
type Detail struct {
	FieldPath  string `json:"field_path,omitempty"`
	ResourceID ir.ID  `json:"resource_id,omitempty"`
}
type SafeError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []Detail `json:"details"`
}

// Safe replaces all caller text with fixed explanations. Raw output and caller
// paths never become persisted diagnostics.
func Safe(code string) *SafeError {
	message, ok := safeMessages[code]
	if !ok {
		code, message = "INTERNAL_ERROR", "The operation could not be completed."
	}
	return &SafeError{Code: code, Message: message, Details: []Detail{}}
}

var safeMessages = map[string]string{
	"CORE_CONFIG_INVALID":    "The generated configuration was rejected by the core.",
	"CORE_BINARY_MISMATCH":   "The registered core binary did not match its build.",
	"RUNNER_RESOURCE_LIMIT":  "The runner could not reserve execution resources.",
	"PORT_BUSY":              "The reserved local port was unavailable.",
	"CANCELED":               "Cancellation was requested.",
	"JOB_TIMEOUT":            "The job exceeded its time limit.",
	"LEASE_LOST":             "The job lease is no longer valid.",
	"INVALID_CONFIG":         "The input configuration is invalid.",
	"CAPABILITY_UNSUPPORTED": "The requested capability is unsupported.",
	"VALIDATION_FAILED":      "The merged resource is not valid.",
	"SERVICE_UNAVAILABLE":    "The operation is temporarily unavailable.",
	"INTERNAL_ERROR":         "The operation could not be completed.",
}

func KnownError(code string) bool { _, ok := safeMessages[code]; return ok }

type Metrics struct {
	ConfigCheckMS  *float64 `json:"config_check_ms,omitempty"`
	CoreStartMS    *float64 `json:"core_start_ms,omitempty"`
	ProxyDialMS    *float64 `json:"proxy_dial_ms,omitempty"`
	TargetTLSMS    *float64 `json:"target_tls_ms,omitempty"`
	HTTPTTFBMS     *float64 `json:"http_ttfb_ms,omitempty"`
	HTTPTotalMS    *float64 `json:"http_total_ms,omitempty"`
	BodyBytes      *int64   `json:"body_bytes,omitempty"`
	BodyDurationMS *float64 `json:"body_duration_ms,omitempty"`
	ThroughputMbps *float64 `json:"throughput_mbps,omitempty"`
	EgressIP       *string  `json:"egress_ip,omitempty"`
	SampleCount    *int32   `json:"sample_count,omitempty"`
	FailureCount   *int32   `json:"failure_count,omitempty"`
}
type EventRequest struct {
	JobID     ir.ID      `json:"job_id"`
	Attempt   int32      `json:"attempt"`
	LeaseSeq  Sequence   `json:"lease_seq"`
	EventID   ir.ID      `json:"event_id"`
	Phase     string     `json:"phase"`
	Completed int32      `json:"completed"`
	Total     int32      `json:"total"`
	Verdict   string     `json:"verdict,omitempty"`
	Error     *SafeError `json:"error,omitempty"`
}
type EventReceipt struct {
	JobID    ir.ID    `json:"job_id"`
	Attempt  int32    `json:"attempt"`
	LeaseSeq Sequence `json:"lease_seq"`
	EventID  ir.ID    `json:"event_id"`
	Seq      Sequence `json:"seq"`
	Replayed bool     `json:"replayed"`
}
type ResultRequest struct {
	JobID      ir.ID      `json:"job_id"`
	Attempt    int32      `json:"attempt"`
	LeaseSeq   Sequence   `json:"lease_seq"`
	ResultHash string     `json:"result_hash"`
	State      string     `json:"state"`
	Verdict    string     `json:"verdict,omitempty"`
	Metrics    Metrics    `json:"metrics"`
	Error      *SafeError `json:"error,omitempty"`
}
type ResultReceipt struct {
	JobID        ir.ID    `json:"job_id"`
	Attempt      int32    `json:"attempt"`
	LeaseSeq     Sequence `json:"lease_seq"`
	ResultID     ir.ID    `json:"result_id"`
	ResultHash   string   `json:"result_hash"`
	Replayed     bool     `json:"replayed"`
	SettledBytes int64    `json:"settled_bytes"`
}
type Response[T any] struct {
	RequestID string `json:"request_id"`
	Data      T      `json:"data"`
}
type LeaseData struct {
	Lease *Lease `json:"lease"`
}

func Digest(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }
func ValidDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && hex.EncodeToString(data) == value
}
func PayloadHash(payload FrozenPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalid
	}
	defer clear(data)
	return Digest(data), nil
}

// ResultHash authenticates the canonical safe outcome, independent of transport
// identity. Both sides compute this exact preimage; the queue verifies it.
func ResultHash(result ResultRequest) (string, error) {
	var safe *SafeError
	if result.Error != nil {
		if !KnownError(result.Error.Code) {
			return "", ErrInvalid
		}
		safe = Safe(result.Error.Code)
	}
	value := struct {
		State   string     `json:"state"`
		Verdict string     `json:"verdict,omitempty"`
		Metrics Metrics    `json:"metrics"`
		Error   *SafeError `json:"error,omitempty"`
	}{result.State, result.Verdict, result.Metrics, safe}
	data, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalid
	}
	return Digest(data), nil
}

// ValidateConfigPayload deliberately accepts only this milestone's offline
// configuration validation. Future network tasks require a separate validator.
func ValidateConfigPayload(payload FrozenPayload) error {
	p := payload.ExecutionPolicy
	if payload.SchemaVersion != 1 || payload.Type != "config_validate" || payload.Core.CoreBuildID.Validate() != nil || !ValidDigest(payload.Core.BuildSHA256) || (payload.QuotaReservationID != "" && payload.QuotaReservationID.Validate() != nil) || payload.Core.Version == "" || payload.Core.AdapterVersion == "" || payload.Core.Platform != "linux" || (payload.Core.Architecture != "amd64" && payload.Core.Architecture != "arm64") || payload.Limits.DurationMS < 1 || payload.Limits.DurationMS > 300000 || payload.Limits.MaxBytes != 0 || p.Network != "none" || p.AllowEnvironmentProxy || p.AllowCoreDownloads || p.AllowShell || p.TerminationGraceMS < 100 || p.TerminationGraceMS > 10000 || p.MemoryLimitBytes < 1048576 || p.MemoryLimitBytes > 2147483647 || p.ProcessLimit < 1 || p.ProcessLimit > 32 {
		return ErrInvalid
	}
	formats := map[ir.CoreFamily]ir.OutputFormat{ir.Xray: ir.XrayJSON, ir.SingBox: ir.SingBoxJSON, ir.Mihomo: ir.MihomoYAML}
	format, ok := formats[payload.Core.CoreFamily]
	a := payload.Artifact
	if !ok || a.Format != format || a.ArtifactID.Validate() != nil || a.ByteLength < 1 || a.ByteLength > 10<<20 || len(a.ContentBase64) > 13981016 || !ValidDigest(a.SHA256) {
		return ErrInvalid
	}
	plain, err := base64.StdEncoding.Strict().DecodeString(a.ContentBase64)
	defer clear(plain)
	if err != nil || int64(len(plain)) != a.ByteLength || Digest(plain) != a.SHA256 {
		return ErrInvalid
	}
	if s := payload.Subject; s != nil && (s.ID.Validate() != nil || (s.Kind != ir.KindNode && s.Kind != ir.KindChain) || s.Revision < 1 || s.SecurityEpoch < 1) {
		return ErrInvalid
	}
	return nil
}
func ValidateConfigMetrics(m Metrics) error {
	if m.CoreStartMS != nil || m.ProxyDialMS != nil || m.TargetTLSMS != nil || m.HTTPTTFBMS != nil || m.HTTPTotalMS != nil || m.BodyBytes != nil || m.BodyDurationMS != nil || m.ThroughputMbps != nil || m.EgressIP != nil || m.SampleCount != nil || m.FailureCount != nil {
		return ErrInvalid
	}
	if m.ConfigCheckMS != nil && (math.IsNaN(*m.ConfigCheckMS) || math.IsInf(*m.ConfigCheckMS, 0) || *m.ConfigCheckMS < 0 || *m.ConfigCheckMS > 300000) {
		return ErrInvalid
	}
	return nil
}
