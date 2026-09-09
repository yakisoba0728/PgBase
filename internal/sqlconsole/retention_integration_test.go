package sqlconsole

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
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

func TestLedgerConcurrentReplaysReleaseAdmissionConnection(t *testing.T) {
	fixture := newPoolFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	blocker, err := fixture.owner.OpenCoordinationSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close(context.Background()) }()
	parsed, err := url.Parse(blocker.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "4")
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ledger, _ := NewLedger(pool)
	identity := ledgerIdentity(fixture.actorID, writeRequest(uuid.New(), "concurrent-replay-key-00001", uuid.New()), []byte("n8-concurrent-replay-secret-long-enough"))
	if _, created, err := ledger.CreatePending(ctx, identity); err != nil || !created {
		t.Fatalf("create=%v err=%v", created, err)
	}
	transaction, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, `lock table pgbase_meta.sql_console_write_identities in access exclusive mode`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			execution, created, err := ledger.CreatePending(ctx, identity)
			if err == nil && (created || execution.ID != identity.Request.ExecutionID) {
				err = errors.New("replay created a new execution")
			}
			results <- err
		}()
	}
	for pool.Stats().AcquiredConns != 4 && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent replay: %v", err)
		}
	}
	if acquired := pool.Stats().AcquiredConns; acquired != 0 {
		t.Fatalf("replay leaked %d connections", acquired)
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
