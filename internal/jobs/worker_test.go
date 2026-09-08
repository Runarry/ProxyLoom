package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type workerRepository struct {
	complete func(context.Context, LeaseIdentity, Result, func(context.Context, pgx.Tx) error) (ResultReceipt, error)
}

func (*workerRepository) Claim(context.Context, ClaimInput) (*Lease, error) { return nil, nil }
func (*workerRepository) Heartbeat(context.Context, LeaseIdentity) (Heartbeat, error) {
	return Heartbeat{}, ErrUnavailable
}
func (*workerRepository) Event(context.Context, LeaseIdentity, EventInput) (EventReceipt, error) {
	return EventReceipt{}, nil
}
func (r *workerRepository) CompleteTx(ctx context.Context, id LeaseIdentity, result Result, callback func(context.Context, pgx.Tx) error) (ResultReceipt, error) {
	return r.complete(ctx, id, result, callback)
}
func testWorkerLease() Lease {
	id := NewID()
	return Lease{Job: Job{ID: id, Type: ImportParse, Executor: APIWorker}, Identity: LeaseIdentity{JobID: id, WorkerID: NewID(), Attempt: 1, LeaseSeq: 1}, Payload: []byte("synthetic-input"), ExpiresAt: time.Now().Add(LeaseDuration)}
}
func TestWorkerStopsOnLocalLeaseExpiryAndClearsPayload(t *testing.T) {
	lease := testWorkerLease()
	lease.ExpiresAt = time.Now().Add(30 * time.Millisecond)
	repo := &workerRepository{complete: func(context.Context, LeaseIdentity, Result, func(context.Context, pgx.Tx) error) (ResultReceipt, error) {
		t.Error("expired worker tried to publish")
		return ResultReceipt{}, nil
	}}
	worker, err := NewWorker(repo, WorkerConfig{WorkerID: lease.Identity.WorkerID, Handlers: map[Type]Handler{ImportParse: func(ctx context.Context, _ Lease) (Result, CommitFunc, error) {
		<-ctx.Done()
		return Result{}, nil, ctx.Err()
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.process(context.Background(), lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("expired local lease did not stop handler", err)
	}
	for _, value := range lease.Payload {
		if value != 0 {
			t.Fatal("worker retained plaintext")
		}
	}
}
func TestWorkerCancellationWinsCompletionRace(t *testing.T) {
	lease := testWorkerLease()
	calls := 0
	repo := &workerRepository{complete: func(_ context.Context, _ LeaseIdentity, result Result, callback func(context.Context, pgx.Tx) error) (ResultReceipt, error) {
		calls++
		if calls == 1 {
			return ResultReceipt{}, ErrCanceled
		}
		if result.State != Canceled || callback != nil {
			t.Error("cancel race submitted successful business changes")
		}
		return ResultReceipt{}, nil
	}}
	worker, err := NewWorker(repo, WorkerConfig{WorkerID: lease.Identity.WorkerID, Handlers: map[Type]Handler{ImportParse: func(context.Context, Lease) (Result, CommitFunc, error) {
		return Result{State: Succeeded, Verdict: Pass}, func(context.Context, pgx.Tx) error { t.Error("unfenced callback ran"); return nil }, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.process(context.Background(), lease); err != nil || calls != 2 {
		t.Fatal("worker did not settle cancellation race", err)
	}
}
func TestWorkerHandlerFailurePreservesInfrastructureClassification(t *testing.T) {
	for _, item := range []struct {
		name  string
		cause error
		code  string
	}{{"invalid", ErrInvalidInput, "INVALID_CONFIG"}, {"infrastructure", errors.New("synthetic-private-database-detail"), "SERVICE_UNAVAILABLE"}} {
		t.Run(item.name, func(t *testing.T) {
			lease := testWorkerLease()
			repo := &workerRepository{complete: func(_ context.Context, _ LeaseIdentity, result Result, callback func(context.Context, pgx.Tx) error) (ResultReceipt, error) {
				if result.State != Failed || result.Error == nil || result.Error.Code != item.code || callback != nil {
					t.Error("handler error classification was lost")
				}
				return ResultReceipt{}, nil
			}}
			worker, err := NewWorker(repo, WorkerConfig{WorkerID: lease.Identity.WorkerID, Handlers: map[Type]Handler{ImportParse: func(context.Context, Lease) (Result, CommitFunc, error) { return Result{}, nil, item.cause }}})
			if err != nil {
				t.Fatal(err)
			}
			if err = worker.process(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
		})
	}
}
