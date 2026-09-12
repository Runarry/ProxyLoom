package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

// Handler performs bounded parsing outside the transaction, then returns the
// small database commit callback. It must observe cancellation and never publish
// its result by itself. The worker owns payload lifetime and clears it on exit.
type Handler func(context.Context, Lease) (Result, CommitFunc, error)
type WorkerConfig struct {
	WorkerID     ir.ID
	Handlers     map[Type]Handler
	PollInterval time.Duration
}
type Worker struct {
	repo   Repository
	config WorkerConfig
	kinds  []Type
}

func NewWorker(repo Repository, config WorkerConfig) (*Worker, error) {
	if repo == nil || config.WorkerID.Validate() != nil || len(config.Handlers) == 0 {
		return nil, ErrInvalidInput
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.PollInterval < 10*time.Millisecond || config.PollInterval > time.Minute {
		return nil, ErrInvalidInput
	}
	copyHandlers := make(map[Type]Handler, len(config.Handlers))
	var kinds []Type
	for kind, handler := range config.Handlers {
		if !ValidType(APIWorker, kind) || handler == nil {
			return nil, ErrInvalidInput
		}
		copyHandlers[kind] = handler
		kinds = append(kinds, kind)
	}
	config.Handlers = copyHandlers
	return &Worker{repo: repo, config: config, kinds: kinds}, nil
}

// Run persists no local queue. Restarted processes resume through the same
// database claim/recovery path. Cancellation returns after active work stops.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lease, err := w.repo.Claim(ctx, ClaimInput{Executor: APIWorker, WorkerID: w.config.WorkerID, Types: w.kinds})
		if err == nil && lease != nil {
			err = w.process(ctx, *lease)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A rejected business callback belongs to that attempt. Leave recovery
		// to its persistent lease instead of terminating the whole API worker.
		if err == nil && lease != nil {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (w *Worker) process(parent context.Context, lease Lease) error {
	defer clear(lease.Payload)
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() { defer close(stopped); w.renew(ctx, cancel, lease, done) }()
	phase := "parsing"
	if lease.Job.Type == Compile {
		phase = "compiling"
	}
	_, err := w.repo.Event(ctx, lease.Identity, EventInput{EventID: NewID(), Phase: phase, Total: 1})
	var result Result
	var commit CommitFunc
	if err == nil {
		result, commit, err = w.config.Handlers[lease.Job.Type](ctx, lease)
	}
	close(done)
	<-stopped
	if parent.Err() != nil {
		return parent.Err()
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrCanceled) {
		result = Result{State: Canceled, Error: runnerprotocol.Safe("CANCELED")}
		commit = nil
		err = nil
	} else if cause != nil {
		return ErrLeaseLost
	}
	if err != nil {
		code := "SERVICE_UNAVAILABLE"
		if errors.Is(err, ErrInvalidInput) {
			code = "INVALID_CONFIG"
		}
		result = Result{State: Failed, Error: runnerprotocol.Safe(code)}
		commit = nil
	}
	_, err = w.repo.CompleteTx(parent, lease.Identity, result, commit)
	// A cancel can win after the last renewal and before atomic publication.
	if errors.Is(err, ErrCanceled) {
		_, err = w.repo.CompleteTx(parent, lease.Identity, Result{State: Canceled, Error: runnerprotocol.Safe("CANCELED")}, nil)
	}
	return err
}
func (w *Worker) renew(ctx context.Context, cancel context.CancelCauseFunc, lease Lease, done <-chan struct{}) {
	expires := lease.ExpiresAt
	deadline := time.NewTimer(time.Until(expires))
	defer deadline.Stop()
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-deadline.C:
			cancel(ErrLeaseLost)
			return
		case <-ticker.C:
			renewCtx, stop := context.WithDeadline(ctx, expires)
			beat, err := w.repo.Heartbeat(renewCtx, lease.Identity)
			stop()
			if err != nil {
				cancel(ErrLeaseLost)
				return
			}
			if beat.CancelRequested {
				cancel(ErrCanceled)
				return
			}
			expires = beat.ExpiresAt
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
			deadline.Reset(time.Until(expires))
		}
	}
}
