// Package jobs defines durable execution and the API worker engine. Runner wire
// values live in runnerprotocol so the Runner never imports database capability.
package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

const LeaseDuration = 30 * time.Second
const HeartbeatInterval = 5 * time.Second
const MaxPayloadBytes = 15 << 20
const EventRetention = 7 * 24 * time.Hour

type Executor string
type Type string
type State string
type Verdict string

const (
	APIWorker          Executor = "api_worker"
	Runner             Executor = "runner"
	ImportParse        Type     = "import_parse"
	ConfigValidate     Type     = "config_validate"
	SourceRefresh      Type     = "source_refresh"
	Compile            Type     = "compile"
	Connectivity       Type     = "connectivity"
	DownloadThroughput Type     = "download_throughput"
	Queued             State    = "queued"
	Leased             State    = "leased"
	Running            State    = "running"
	Succeeded          State    = "succeeded"
	Failed             State    = "failed"
	Canceled           State    = "canceled"
	TimedOut           State    = "timed_out"
	Pass               Verdict  = "pass"
	Fail               Verdict  = "fail"
	Inconclusive       Verdict  = "inconclusive"
)

var (
	ErrInvalidInput     = errors.New("jobs_invalid_input")
	ErrUnavailable      = errors.New("jobs_unavailable")
	ErrNotFound         = errors.New("jobs_not_found")
	ErrLeaseLost        = errors.New("jobs_lease_lost")
	ErrConflict         = errors.New("jobs_state_conflict")
	ErrRevisionConflict = errors.New("jobs_revision_conflict")
	ErrCanceled         = errors.New("jobs_canceled")
)

func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == Canceled || s == TimedOut
}
func (s State) Valid() bool { return s == Queued || s == Leased || s == Running || s.Terminal() }
func ValidType(executor Executor, kind Type) bool {
	return executor == APIWorker && (kind == ImportParse || kind == SourceRefresh || kind == Compile) || executor == Runner && kind == ConfigValidate
}
func (kind Type) Valid() bool {
	return kind == ImportParse || kind == ConfigValidate || kind == SourceRefresh || kind == Compile || kind == Connectivity || kind == DownloadThroughput
}

type Job struct {
	ID              ir.ID
	ScopeID         ir.ID
	BatchID         ir.ID
	Revision        int64
	Executor        Executor
	Type            Type
	State           State
	Attempt         int32
	LeaseSeq        int64
	CancelRequested bool
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CoreBuildID     ir.ID
	Verdict         Verdict
	Error           *runnerprotocol.SafeError
}
type EnqueueInput struct {
	ID          ir.ID
	ScopeID     ir.ID
	BatchID     ir.ID
	Executor    Executor
	Type        Type
	Payload     []byte
	CoreBuildID ir.ID
}

func (EnqueueInput) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (EnqueueInput) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

type ClaimInput struct {
	Executor     Executor
	WorkerID     ir.ID
	Types        []Type
	CoreBuildIDs []ir.ID
}
type LeaseIdentity struct {
	JobID    ir.ID
	WorkerID ir.ID
	Attempt  int32
	LeaseSeq int64
}

func (id LeaseIdentity) Valid() bool {
	return id.JobID.Validate() == nil && id.WorkerID.Validate() == nil && id.Attempt > 0 && id.Attempt <= 100 && id.LeaseSeq > 0
}

type Lease struct {
	Job       Job
	Identity  LeaseIdentity
	Payload   []byte
	ExpiresAt time.Time
}

func (Lease) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (Lease) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

type Heartbeat struct {
	ExpiresAt       time.Time
	CancelRequested bool
}
type EventInput struct {
	EventID   ir.ID
	Phase     string
	Completed int32
	Total     int32
	Verdict   Verdict
	Error     *runnerprotocol.SafeError
}
type Event struct {
	JobID     ir.ID
	Seq       int64
	Phase     string
	Completed int32
	Total     int32
	Verdict   Verdict
	Error     *runnerprotocol.SafeError
}
type EventReceipt = runnerprotocol.EventReceipt
type Result struct {
	State   State
	Verdict Verdict
	Metrics runnerprotocol.Metrics
	Error   *runnerprotocol.SafeError
	Hash    string
}
type ResultReceipt = runnerprotocol.ResultReceipt
type CommitFunc func(context.Context, pgx.Tx) error
type Repository interface {
	Claim(context.Context, ClaimInput) (*Lease, error)
	Heartbeat(context.Context, LeaseIdentity) (Heartbeat, error)
	Event(context.Context, LeaseIdentity, EventInput) (EventReceipt, error)
	CompleteTx(context.Context, LeaseIdentity, Result, func(context.Context, pgx.Tx) error) (ResultReceipt, error)
}
type ListInput struct {
	ScopeID        ir.ID
	State          State
	Type           Type
	Executor       Executor
	BatchID        ir.ID
	Limit          int
	AfterCreatedAt time.Time
	AfterID        ir.ID
}
type Page struct {
	Jobs    []Job
	HasMore bool
}
type EventPage struct {
	Events    []Event
	LatestSeq int64
	Reset     bool
	Terminal  bool
}
type Batch struct {
	ID              ir.ID
	ScopeID         ir.ID
	Revision        int64
	State           State
	Verdict         Verdict
	Children        []Job
	Completed       int32
	CancelRequested bool
	EffectiveLimits runnerprotocol.Limits
	CreatedAt       time.Time
}
type BatchInput struct {
	ID              ir.ID
	ScopeID         ir.ID
	EffectiveLimits runnerprotocol.Limits
	Children        []EnqueueInput
}
type Snapshot struct {
	Job   *Job
	Batch *Batch
}
type ManagementRepository interface {
	Get(context.Context, ir.ID, ir.ID) (Job, error)
	List(context.Context, ListInput) (Page, error)
	Snapshot(context.Context, ir.ID, ir.ID) (Snapshot, error)
	Cancel(context.Context, ir.ID, ir.ID, int64) (Snapshot, error)
	Events(context.Context, ir.ID, ir.ID, int64, int) (EventPage, error)
}

func NewID() ir.ID {
	var value [16]byte
	_, _ = rand.Read(value[:]) // crypto/rand.Read fills the slice or terminates.
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	return ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]))
}
