package sqlconsole

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestServiceNeverReplaysExpiredWriteWithoutReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status Status
	}{
		{"unknown", ErrCommitUnknown, StatusUnknown},
		{"failed", errors.New("write failed"), StatusFailed},
		{"canceled", context.Canceled, StatusCanceled},
		{"timed_out", context.DeadlineExceeded, StatusTimedOut},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPoolFixture(t)
			ledger, _ := NewLedger(fixture.owner)
			executor := &retentionExecutor{err: test.err}
			secret := []byte("n8-retention-service-secret-long-enough")
			service, err := NewService(context.Background(), executor, ledger, secret, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = service.Close(context.Background()) }()
			actor := testActor(fixture.actorID, "retention-session")
			request := writeRequest(uuid.New(), "retained-write-identity-0000001", uuid.New())
			if _, err := service.Submit(context.Background(), actor, request); err != nil {
				t.Fatal(err)
			}
			waitForStatus(t, service, actor, request.ExecutionID, test.status)
			if err := service.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.owner.Exec(context.Background(), `update pgbase_meta.sql_console_executions set
				created_at=current_timestamp-interval '32 days',started_at=current_timestamp-interval '32 days',
				finished_at=current_timestamp-interval '31 days',result_expires_at=current_timestamp-interval '31 days'
				where execution_id=$1`, request.ExecutionID); err != nil {
				t.Fatal(err)
			}
			if err := ledger.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			service, err = NewService(context.Background(), executor, ledger, secret, true)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := service.Submit(context.Background(), actor, request)
			if err != nil || replayed.Status != test.status || executor.calls.Load() != 1 {
				t.Fatalf("expired replay status=%s err=%v executor calls=%d", replayed.Status, err, executor.calls.Load())
			}
			page, err := service.List(context.Background(), actor, Cursor{})
			if err != nil || len(page.Executions) != 0 {
				t.Fatalf("expired history=%#v err=%v", page, err)
			}
			request.Statements[0].SQL = "delete from ext.items"
			if _, err := service.Submit(context.Background(), actor, request); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed request after cleanup=%v", err)
			}
		})
	}
}

type retentionExecutor struct {
	calls atomic.Int32
	err   error
}

func (*retentionExecutor) Validate(context.Context, Request) error { return nil }
func (executor *retentionExecutor) Run(context.Context, RunInput) (Result, error) {
	executor.calls.Add(1)
	return Result{Elapsed: time.Millisecond}, executor.err
}
