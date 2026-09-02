//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestListenerRecordsConnectReloadAndReadinessTransitions(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	recorder := &schemaTelemetryRecorder{}
	listener := NewListener(pool, &Cache{}, recorder)
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(runCtx) }()
	waitForListenerReady(t, listener)
	cancelRun()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}

	events := recorder.snapshot()
	var connectSuccess, reloadSuccess int
	readiness := make([]bool, 0, 2)
	for _, event := range events {
		switch event.Kind() {
		case observability.EventKindOperation:
			if event.Component() != observability.ComponentSchemaListener {
				t.Fatalf("listener operation component = %q", event.Component())
			}
			if event.Operation() == observability.OperationListenerConnect && event.Outcome() == observability.OutcomeSuccess {
				connectSuccess++
			}
			if event.Operation() == observability.OperationListenerReload && event.Outcome() == observability.OutcomeSuccess {
				reloadSuccess++
			}
			if event.Duration() < 0 {
				t.Fatalf("negative listener operation duration: %v", event.Duration())
			}
		case observability.EventKindReadiness:
			if event.ReadinessComponent() != observability.ReadinessSchemaListener {
				t.Fatalf("readiness component = %q", event.ReadinessComponent())
			}
			readiness = append(readiness, event.Ready())
		}
	}
	if connectSuccess != 1 || reloadSuccess != 1 {
		t.Fatalf("listener operation telemetry connect=%d reload=%d events=%#v", connectSuccess, reloadSuccess, events)
	}
	if len(readiness) != 2 || !readiness[0] || readiness[1] {
		t.Fatalf("readiness transitions = %v, want [true false] without duplicates", readiness)
	}

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listener.reload(canceled, connection.Conn()); !errors.Is(err, context.Canceled) {
		connection.Release()
		t.Fatalf("reload(canceled) error = %v", err)
	}
	connection.Release()
	events = recorder.snapshot()
	last := events[len(events)-1]
	if last.Operation() != observability.OperationListenerReload || last.Outcome() != observability.OutcomeCanceled {
		t.Fatalf("canceled reload telemetry = %#v", last)
	}

	pool.Close()
	if _, err := listener.listenOnce(context.Background()); err == nil {
		t.Fatal("listenOnce() with closed pool unexpectedly succeeded")
	}
	events = recorder.snapshot()
	last = events[len(events)-1]
	if last.Operation() != observability.OperationListenerConnect || last.Outcome() == observability.OutcomeSuccess {
		t.Fatalf("failed connect telemetry = %#v", last)
	}
}

func TestListenerSynchronizesTwoCachesAndReloadsMissedVersionBeforeReconnectReady(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	firstPool := openSchemaListenerPool(t, databaseURL)
	secondPool := openSchemaListenerPool(t, databaseURL)
	if err := migrate.New(firstPool, nil).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000002")
	if _, err := firstPool.Exec(ctx, `insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, 'listener-test@example.com', 'test', true, current_timestamp, current_timestamp)`, administratorID); err != nil {
		t.Fatalf("insert administrator: %v", err)
	}

	firstCache := &Cache{}
	secondCache := &Cache{}
	firstService := NewService(firstPool, firstCache, nil)
	firstListener := NewListener(firstPool, firstCache, nil)
	secondListener := NewListener(secondPool, secondCache, nil)
	secondListener.retryMin = 750 * time.Millisecond
	secondListener.retryMax = 750 * time.Millisecond
	reloadedBeforeReady := make(chan struct{})
	allowReady := make(chan struct{})
	secondListener.afterInitialReload = func(catalog Catalog) {
		if catalog.Version != 2 {
			return
		}
		close(reloadedBeforeReady)
		<-allowReady
	}

	runCtx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 2)
	go func() { results <- firstListener.Run(runCtx) }()
	go func() { results <- secondListener.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			if err := <-results; err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("listener run: %v", err)
			}
		}
	})
	// Cleanups run last registered first, so this one releases a listener parked
	// in afterInitialReload before the drain above waits for both Run results.
	// afterInitialReload has no run-context case, so cancel() alone cannot free it.
	t.Cleanup(func() {
		select {
		case <-allowReady:
		default:
			close(allowReady)
		}
	})
	waitForListenerReady(t, firstListener)
	waitForListenerReady(t, secondListener)

	applyFreshPlan(t, firstService, CreateCollectionInput{Name: "posts"}, administratorID)
	waitForCatalogVersion(t, secondCache, 1)

	pid := secondListener.pid.Load()
	if pid == 0 {
		t.Fatal("second listener did not publish its dedicated backend PID")
	}
	var terminated bool
	if err := firstPool.QueryRow(ctx, `select pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
		t.Fatalf("terminate listener backend: %v", err)
	}
	if !terminated {
		t.Fatal("listener backend was not terminated")
	}
	waitForListenerUnavailable(t, secondListener)

	applyFreshPlan(t, firstService, CreateCollectionInput{Name: "pages"}, administratorID)
	if got := secondCache.Load().Version; got != 1 {
		t.Fatalf("disconnected cache version = %d, want unchanged version 1", got)
	}

	select {
	case <-reloadedBeforeReady:
	case <-time.After(10 * time.Second):
		t.Fatal("listener did not reload the missed catalog")
	}
	if err := secondListener.Ready(context.Background()); err == nil {
		t.Fatal("listener became ready before the missed catalog reload completed")
	}
	if _, ok := secondCache.Load().Collections["pages"]; !ok {
		t.Fatal("missed collection was not published before readiness")
	}
	close(allowReady)
	waitForListenerReady(t, secondListener)
}

func TestListenerReadyFailsUntilInitialCatalogSynchronization(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	listener := NewListener(pool, &Cache{}, nil)
	if err := listener.Ready(context.Background()); err == nil {
		t.Fatal("listener reported ready before Run synchronized the catalog")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	waitForListenerReady(t, listener)
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("listener run: %v", err)
	}
	if err := listener.Ready(context.Background()); err == nil {
		t.Fatal("listener remained ready after Run stopped")
	}
}
