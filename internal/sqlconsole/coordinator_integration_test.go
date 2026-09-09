package sqlconsole

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestServiceFencesCoordinatorLossUntilInflightWriteSettles(t *testing.T) {
	fixture := newRunnerFixture(t)
	ledger, _ := NewLedger(fixture.pool.owner)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	fixture.runner.commit = func(ctx context.Context, transaction pgx.Tx) error {
		close(entered)
		<-release
		return transaction.Commit(ctx)
	}
	serviceA, err := NewService(context.Background(), fixture.runner, ledger, []byte("n8-coordinator-loss-secret-long-enough"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); _ = serviceA.Close(context.Background()) }()
	actor := testActor(fixture.pool.actorID, "coordinator-loss-session")
	request := writeRequest(uuid.New(), "coordinator-loss-write-00001", uuid.New())
	if _, err := serviceA.Submit(context.Background(), actor, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not reach commit")
	}
	var terminated bool
	if err := fixture.pool.owner.QueryRow(context.Background(), `select pg_catalog.pg_terminate_backend($1)`, serviceA.coordinator.pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate owned coordinator=%v err=%v", terminated, err)
	}
	type startup struct {
		service *Service
		err     error
	}
	ready := make(chan startup, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		service, err := NewService(ctx, newBlockingExecutor(), ledger, []byte("n8-coordinator-loss-secret-long-enough"), false)
		ready <- startup{service, err}
	}()
	select {
	case result := <-ready:
		if result.service != nil {
			_ = result.service.Close(context.Background())
		}
		t.Fatalf("replacement reconciled before active write settled: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := serviceA.Submit(context.Background(), actor, Request{ExecutionID: uuid.New(), Statements: []Statement{{SQL: "select 1"}}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("unfenced coordinator accepted new work: %v", err)
	}
	unblock()
	result := <-ready
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer func() { _ = result.service.Close(context.Background()) }()
	durable, err := ledger.Get(context.Background(), actor.AdministratorID, request.ExecutionID)
	if err != nil || durable.Status != StatusSucceeded || durable.Result == nil || durable.Result.Committed == nil || !*durable.Result.Committed {
		t.Fatalf("committed write after coordinator loss=%#v err=%v", durable, err)
	}
}
