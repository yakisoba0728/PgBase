package sqlconsole

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

	nextRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	if _, err := service.Submit(context.Background(), actor, nextRequest); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, service, actor, nextRequest.ExecutionID, StatusSucceeded)
	page, err := service.List(context.Background(), actor, Cursor{})
	if err != nil || len(page.Executions) < 5 {
		t.Fatalf("history = %#v, %v", page, err)
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

func (executor *blockingExecutor) Validate(Request) error { return nil }

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
