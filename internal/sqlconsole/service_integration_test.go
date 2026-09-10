//go:build integration

package sqlconsole

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestServiceEnforcesConcurrencyAdmissionAndSessionOwnership(t *testing.T) {
	pool := newPoolFixture(t)
	ledger, _ := NewLedger(pool.owner)
	executor := newBlockingExecutor()
	service, err := NewService(context.Background(), executor, ledger, []byte("n8-service-test-secret-that-is-long-enough"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		executor.releaseAll()
		closeContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Close(closeContext)
	})
	actorA := testActor(pool.actorID, "session-a")
	actorB := testActor(pool.actorID, "session-b")

	type submitted struct {
		actor   Actor
		request Request
	}
	jobs := make([]submitted, 0, MaximumGlobalRunning+MaximumPending)
	for index := 0; index < MaximumGlobalRunning+MaximumPending; index++ {
		actor := actorA
		marker := "a"
		if index%2 == 1 {
			actor = actorB
			marker = "b"
		}
		request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1 /* " + marker + " */"}}}
		if _, err := service.Submit(context.Background(), actor, request); err != nil {
			t.Fatalf("Submit %d: %v", index, err)
		}
		jobs = append(jobs, submitted{actor: actor, request: request})
	}
	executor.waitRunning(t, MaximumGlobalRunning)
	if global, perA, perB := executor.maximums(); global != MaximumGlobalRunning || perA > MaximumSessionRunning || perB > MaximumSessionRunning {
		t.Fatalf("maximum running global=%d sessionA=%d sessionB=%d", global, perA, perB)
	}
	overflow := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	started := time.Now()
	if _, err := service.Submit(context.Background(), actorA, overflow); !errors.Is(err, ErrBusy) {
		t.Fatalf("overflow error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < AdmissionTimeout-25*time.Millisecond || elapsed > time.Second {
		t.Fatalf("admission elapsed = %s", elapsed)
	}

	var pending submitted
	for _, candidate := range jobs {
		execution, err := service.Get(context.Background(), candidate.actor, candidate.request.ExecutionID)
		if err == nil && execution.Status == StatusPending {
			pending = candidate
			break
		}
	}
	if pending.request.ExecutionID == uuid.Nil {
		t.Fatal("no pending execution found")
	}
	wrongSession := actorA
	if pending.actor.SessionKey == actorA.SessionKey {
		wrongSession = actorB
	}
	if _, err := service.Cancel(context.Background(), wrongSession, pending.request.ExecutionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-session cancel error = %v", err)
	}
	if _, err := service.Cancel(context.Background(), pending.actor, pending.request.ExecutionID); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, pending.actor, pending.request.ExecutionID, StatusCanceled)

	executor.releaseAll()
	for _, job := range jobs {
		if job.request.ExecutionID == pending.request.ExecutionID {
			continue
		}
		waitForTerminal(t, service, job.actor, job.request.ExecutionID)
	}
}

func TestServicePersistsRejectReplayResultExpiryAndActualCancel(t *testing.T) {
	fixture := newRunnerFixture(t)
	ledger, _ := NewLedger(fixture.pool.owner)
	service, err := NewService(context.Background(), fixture.runner, ledger, []byte("n8-real-service-secret-that-is-long-enough"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Close(closeContext)
	})
	actor := testActor(fixture.pool.actorID, "real-session")
	otherSession := testActor(fixture.pool.actorID, "other-session")

	rejectedRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select * from pg_catalog.pg_tables"}}}
	rejected, err := service.Submit(context.Background(), actor, rejectedRequest)
	if err != nil || rejected.Status != StatusFailed || rejected.Failure == nil || rejected.Failure.Code != "rejected" {
		t.Fatalf("rejected submission = %#v, %v", rejected, err)
	}

	readRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 9007199254740993::numeric"}}}
	if _, err := service.Submit(context.Background(), actor, readRequest); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Submit(context.Background(), actor, readRequest)
	if err != nil || replayed.ID != readRequest.ExecutionID {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	read := waitForStatus(t, service, actor, readRequest.ExecutionID, StatusSucceeded)
	if read.Result == nil || len(read.Result.Statements) != 1 || read.Result.Statements[0].Rows[0][0].Text != "9007199254740993" {
		t.Fatalf("read result = %#v", read.Result)
	}
	if _, err := service.Get(context.Background(), otherSession, readRequest.ExecutionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-session get error = %v", err)
	}

	service.mu.Lock()
	service.jobs[readRequest.ExecutionID].execution.ResultUntil = time.Now().Add(-time.Second)
	service.mu.Unlock()
	expired, err := service.Get(context.Background(), actor, readRequest.ExecutionID)
	if err != nil || expired.Result == nil || expired.Result.Statements != nil || expired.Result.Rows != 1 {
		t.Fatalf("expired durable result = %#v, %v", expired, err)
	}

	cancelRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Timeout: 10 * time.Second, Statements: []Statement{{SQL: "select pg_catalog.pg_sleep(10)"}}}
	if _, err := service.Submit(context.Background(), actor, cancelRequest); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, cancelRequest.ExecutionID, StatusRunning)
	if _, err := service.Cancel(context.Background(), actor, cancelRequest.ExecutionID); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, cancelRequest.ExecutionID, StatusCanceled)

	itemID := uuid.New()
	write := writeRequest(uuid.New(), "service-write-000000000000001", itemID)
	if _, err := service.Submit(context.Background(), actor, write); err != nil {
		t.Fatal(err)
	}
	written := waitForStatus(t, service, actor, write.ExecutionID, StatusSucceeded)
	if written.Result == nil || written.Result.Committed == nil || !*written.Result.Committed || written.Result.AffectedRows != 2 {
		t.Fatalf("written result = %#v", written.Result)
	}
	var amount string
	if err := fixture.pool.owner.QueryRow(context.Background(), `select amount::text from ext.items where id=$1`, itemID).Scan(&amount); err != nil || amount != "3.7500" {
		t.Fatalf("written amount = %q, %v", amount, err)
	}
	service.mu.Lock()
	delete(service.jobs, write.ExecutionID)
	service.mu.Unlock()
	if _, err := fixture.pool.owner.Exec(context.Background(), `update pgbase_meta.sql_console_executions set
		created_at=current_timestamp-interval '32 days',started_at=current_timestamp-interval '32 days',
		finished_at=current_timestamp-interval '31 days',result_expires_at=current_timestamp-interval '31 days',
		updated_at=current_timestamp where execution_id=$1`, write.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Get(context.Background(), actor.AdministratorID, write.ExecutionID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired heavy execution error = %v", err)
	}
	replayedWrite, err := service.Submit(context.Background(), actor, write)
	if err != nil || replayedWrite.Status != StatusSucceeded || replayedWrite.Result == nil || replayedWrite.Result.Committed == nil || !*replayedWrite.Result.Committed {
		t.Fatalf("expired write replay = %#v, %v", replayedWrite, err)
	}
	newIdentity := write
	newIdentity.ExecutionID = uuid.New()
	if _, err := service.Submit(context.Background(), actor, newIdentity); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired write with a new operation ID error = %v", err)
	}
	var matchingRows, receipts int
	if err := fixture.pool.owner.QueryRow(context.Background(), `select count(*) from ext.items where id=$1`, itemID).Scan(&matchingRows); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.owner.QueryRow(context.Background(), `select count(*) from pgbase_meta.sql_console_write_receipts where execution_id=$1`, write.ExecutionID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if HistoryRetention != 30*24*time.Hour || DurableWriteReceiptRetention != "indefinite" || matchingRows != 1 || receipts != 1 {
		t.Fatalf("durable replay guard history=%s receipt=%q rows=%d receipts=%d", HistoryRetention, DurableWriteReceiptRetention, matchingRows, receipts)
	}

	nextRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	if _, err := service.Submit(context.Background(), actor, nextRequest); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, nextRequest.ExecutionID, StatusSucceeded)
	page, err := service.List(context.Background(), actor, Cursor{})
	if err != nil || len(page.Executions) < 4 {
		t.Fatalf("history = %#v, %v", page, err)
	}
	for _, execution := range page.Executions {
		if execution.ID == write.ExecutionID {
			t.Fatal("expired heavy write execution remained in history")
		}
	}
}

func TestServicePersistsAmbiguousCommitAsUnknown(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.runner.commit = func(context.Context, pgx.Tx) error { return io.ErrUnexpectedEOF }
	ledger, err := NewLedger(fixture.pool.owner)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(context.Background(), fixture.runner, ledger, []byte("n8-unknown-service-secret-long-enough"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Close(ctx)
	}()
	actor := testActor(fixture.pool.actorID, "unknown-session")
	itemID := uuid.New()
	request := writeRequest(uuid.New(), "service-unknown-00000000001", itemID)
	if _, err := service.Submit(context.Background(), actor, request); err != nil {
		t.Fatal(err)
	}
	execution := waitForStatus(t, service, actor, request.ExecutionID, StatusUnknown)
	if execution.Result == nil || execution.Result.Committed != nil || execution.Failure == nil || execution.Failure.Code != "commit_unknown" {
		t.Fatalf("unknown execution = %#v", execution)
	}
	durable, err := ledger.Get(context.Background(), actor.AdministratorID, request.ExecutionID)
	if err != nil || durable.Status != StatusUnknown || durable.Result == nil || durable.Result.Committed != nil {
		t.Fatalf("durable unknown = %#v, %v", durable, err)
	}
	assertItemAbsent(t, fixture.pool, itemID)
}

func TestServiceStartupReconcilesPriorCommitUnknownWithoutRestoringBoundedRows(t *testing.T) {
	fixture := newRunnerFixture(t)
	ledger, err := NewLedger(fixture.pool.owner)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("n8-startup-reconcile-secret-long-enough")
	actor := testActor(fixture.pool.actorID, "startup-reconcile-session")

	committedRequest := writeRequest(uuid.New(), "startup-committed-write-00001", uuid.New())
	committedIdentity := ledgerIdentity(actor.AdministratorID, committedRequest, secret)
	committed, created, err := ledger.CreatePending(context.Background(), committedIdentity)
	if err != nil || !created {
		t.Fatalf("create committed pending = %#v, %v, %v", committed, created, err)
	}
	committed, err = ledger.MarkRunning(context.Background(), actor.AdministratorID, committed.ID)
	if err != nil {
		t.Fatal(err)
	}
	committed.Status = StatusUnknown
	committed.Result = &Result{Rows: 2, AffectedRows: 3, Bytes: 41}
	committed.Failure = &Failure{Code: "commit_unknown"}
	if err := ledger.Finish(context.Background(), actor.AdministratorID, committed); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.owner.Exec(context.Background(), `insert into pgbase_meta.sql_console_write_receipts
		(execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
		values($1,$2,$3,$4,2,3,41,current_timestamp)`, committedRequest.ExecutionID, actor.AdministratorID,
		committedIdentity.IdempotencyHash[:], committedIdentity.RequestHash[:]); err != nil {
		t.Fatal(err)
	}

	unknownRequest := writeRequest(uuid.New(), "startup-unknown-write-0000001", uuid.New())
	unknownIdentity := ledgerIdentity(actor.AdministratorID, unknownRequest, secret)
	unknown, created, err := ledger.CreatePending(context.Background(), unknownIdentity)
	if err != nil || !created {
		t.Fatalf("create unknown pending = %#v, %v, %v", unknown, created, err)
	}
	unknown, err = ledger.MarkRunning(context.Background(), actor.AdministratorID, unknown.ID)
	if err != nil {
		t.Fatal(err)
	}
	unknown.Status = StatusUnknown
	unknown.Result = &Result{}
	unknown.Failure = &Failure{Code: "commit_unknown"}
	if err := ledger.Finish(context.Background(), actor.AdministratorID, unknown); err != nil {
		t.Fatal(err)
	}

	service, err := NewService(context.Background(), fixture.runner, ledger, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	reconciled, err := service.Get(context.Background(), actor, committedRequest.ExecutionID)
	if err != nil || reconciled.Status != StatusSucceeded || reconciled.Result == nil || reconciled.Result.Committed == nil || !*reconciled.Result.Committed || reconciled.Result.Rows != 2 || reconciled.Result.AffectedRows != 3 || reconciled.Result.Bytes != 41 {
		t.Fatalf("startup reconciled execution = %#v, %v", reconciled, err)
	}
	if reconciled.Result.Statements != nil || reconciled.ResultUntil.Before(time.Now().UTC()) {
		t.Fatalf("restart restored bounded rows or an expired result TTL: %#v", reconciled.Result)
	}

	preserved, err := service.Get(context.Background(), actor, unknownRequest.ExecutionID)
	if err != nil || preserved.Status != StatusUnknown || preserved.Result == nil || preserved.Result.Committed != nil || preserved.Failure == nil || preserved.Failure.Code != "commit_unknown" {
		t.Fatalf("startup preserved unresolved execution = %#v, %v", preserved, err)
	}
}

func TestServiceCoordinatorPreventsLiveExecutionReconciliation(t *testing.T) {
	fixture := newPoolFixture(t)
	ledger, err := NewLedger(fixture.owner)
	if err != nil {
		t.Fatal(err)
	}
	executor := newBlockingExecutor()
	serviceA, err := NewService(context.Background(), executor, ledger, []byte("n8-coordinator-service-secret-long-enough"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		executor.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = serviceA.Close(ctx)
	}()
	actor := testActor(fixture.actorID, "coordinator-session")
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1 /* a */"}}}
	if _, err := serviceA.Submit(context.Background(), actor, request); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, serviceA, actor, request.ExecutionID, StatusRunning)
	if serviceB, err := NewService(context.Background(), newBlockingExecutor(), ledger, []byte("n8-second-service-secret-long-enough"), false); !errors.Is(err, ErrCoordinatorBusy) {
		if serviceB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = serviceB.Close(ctx)
			cancel()
		}
		t.Fatalf("second coordinator error = %v", err)
	}
	durable, err := ledger.Get(context.Background(), actor.AdministratorID, request.ExecutionID)
	if err != nil || durable.Status != StatusRunning {
		t.Fatalf("live durable execution = %#v, %v", durable, err)
	}
	executor.releaseAll()
	waitForStatus(t, serviceA, actor, request.ExecutionID, StatusSucceeded)
	durable, err = ledger.Get(context.Background(), actor.AdministratorID, request.ExecutionID)
	if err != nil || durable.Status != StatusSucceeded {
		t.Fatalf("completed durable execution = %#v, %v", durable, err)
	}
}

func TestServiceSurfacesTerminalLedgerFailure(t *testing.T) {
	fixture := newPoolFixture(t)
	ledger, _ := NewLedger(fixture.owner)
	executor := newBlockingExecutor()
	service, err := NewService(context.Background(), executor, ledger, []byte("n8-terminal-ledger-secret-long-enough"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		executor.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Close(ctx)
	}()
	actor := testActor(fixture.actorID, "terminal-ledger-session")
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1 /* a */"}}}
	if _, err := service.Submit(context.Background(), actor, request); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, request.ExecutionID, StatusRunning)
	if _, err := fixture.owner.Exec(context.Background(), `update pgbase_meta.sql_console_executions set
		status='failed',failure_code='forced_fixture',committed=false,updated_at=current_timestamp,
		finished_at=current_timestamp,result_expires_at=current_timestamp+interval '5 minutes'
		where execution_id=$1`, request.ExecutionID); err != nil {
		t.Fatal(err)
	}
	executor.releaseAll()
	deadline := time.Now().Add(3 * time.Second)
	for {
		service.mu.Lock()
		persistErr := service.jobs[request.ExecutionID].persistErr
		service.mu.Unlock()
		if persistErr != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal ledger failure was discarded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := service.Get(context.Background(), actor, request.ExecutionID); !errors.Is(err, ErrClosed) {
		t.Fatalf("terminal ledger Get error = %v", err)
	}
	if _, err := service.Cancel(context.Background(), actor, request.ExecutionID); !errors.Is(err, ErrClosed) {
		t.Fatalf("terminal ledger Cancel error = %v", err)
	}
	if _, err := service.replayed(actor, Execution{ID: request.ExecutionID}); !errors.Is(err, ErrClosed) {
		t.Fatalf("terminal ledger replay error = %v", err)
	}
}

func TestServicePublishesTerminalOnlyAfterLedgerFinish(t *testing.T) {
	fixture := newPoolFixture(t)
	ledger, _ := NewLedger(fixture.owner)
	executor := newBlockingExecutor()
	service, err := NewService(context.Background(), executor, ledger, []byte("n8-terminal-publication-secret-long-enough"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		executor.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Close(ctx)
	})
	ctx := context.Background()
	actor := testActor(fixture.actorID, "terminal-publication-session")
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	if _, err := service.Submit(ctx, actor, request); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, request.ExecutionID, StatusRunning)
	connection, err := fixture.owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `select execution_id from pgbase_meta.sql_console_executions where execution_id=$1 for update`, request.ExecutionID); err != nil {
		t.Fatal(err)
	}
	executor.releaseAll()
	deadline := time.Now().Add(time.Second)
	for {
		var blocked bool
		if err := fixture.owner.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where $1::integer=any(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal ledger write did not wait on controlled row lock")
		}
		time.Sleep(time.Millisecond)
	}
	visible, err := service.Get(ctx, actor, request.ExecutionID)
	if err != nil || visible.Status != StatusRunning {
		t.Fatalf("terminal status visible before durable finish: %#v, %v", visible, err)
	}
	if canceled, err := service.Cancel(ctx, actor, request.ExecutionID); err != nil || canceled.Status != StatusRunning {
		t.Fatalf("cancel exposed an unpersisted terminal result: %#v, %v", canceled, err)
	}
	if replayed, err := service.replayed(actor, Execution{ID: request.ExecutionID}); err != nil || replayed.Status != StatusRunning {
		t.Fatalf("replay exposed an unpersisted terminal result: %#v, %v", replayed, err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, request.ExecutionID, StatusSucceeded)
	durable, err := ledger.Get(ctx, actor.AdministratorID, request.ExecutionID)
	if err != nil || durable.Status != StatusSucceeded {
		t.Fatalf("visible success without durable success: %#v, %v", durable, err)
	}
}

type blockingExecutor struct {
	mu        sync.Mutex
	release   chan struct{}
	released  bool
	running   int
	maximum   int
	byMarker  map[string]int
	maxMarker map[string]int
}

func newBlockingExecutor() *blockingExecutor {
	return &blockingExecutor{release: make(chan struct{}), byMarker: make(map[string]int), maxMarker: make(map[string]int)}
}

func (executor *blockingExecutor) Validate(context.Context, Request) error { return nil }

func (executor *blockingExecutor) Run(ctx context.Context, input RunInput) (Result, error) {
	marker := "a"
	if len(input.Request.Statements) > 0 && strings.HasSuffix(input.Request.Statements[0].SQL, "b */") {
		marker = "b"
	}
	executor.mu.Lock()
	executor.running++
	executor.byMarker[marker]++
	if executor.running > executor.maximum {
		executor.maximum = executor.running
	}
	if executor.byMarker[marker] > executor.maxMarker[marker] {
		executor.maxMarker[marker] = executor.byMarker[marker]
	}
	executor.mu.Unlock()
	defer func() {
		executor.mu.Lock()
		executor.running--
		executor.byMarker[marker]--
		executor.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-executor.release:
		committed := false
		return Result{Committed: &committed}, nil
	}
}

func (executor *blockingExecutor) waitRunning(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		executor.mu.Lock()
		running := executor.running
		executor.mu.Unlock()
		if running == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("executor did not reach %d running", want)
}

func (executor *blockingExecutor) maximums() (int, int, int) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.maximum, executor.maxMarker["a"], executor.maxMarker["b"]
}

func (executor *blockingExecutor) releaseAll() {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if !executor.released {
		close(executor.release)
		executor.released = true
	}
}

func testActor(adminID uuid.UUID, session string) Actor {
	return Actor{AdministratorID: adminID, SessionKey: DeriveSessionKey([]byte("n8-actor-derivation-secret-long-enough"), []byte(session))}
}

func waitForStatus(t *testing.T, service *Service, actor Actor, id uuid.UUID, want Status) Execution {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		execution, err := service.Get(context.Background(), actor, id)
		if err == nil && execution.Status == want {
			return execution
		}
		time.Sleep(10 * time.Millisecond)
	}
	execution, err := service.Get(context.Background(), actor, id)
	t.Fatalf("execution %s = %#v, %v; want %s", id, execution, err, want)
	return Execution{}
}

func waitForTerminal(t *testing.T, service *Service, actor Actor, id uuid.UUID) Execution {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		execution, err := service.Get(context.Background(), actor, id)
		if err == nil && terminalStatus(execution.Status) {
			return execution
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("execution %s did not finish", id)
	return Execution{}
}
