package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

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
	firstService := NewService(firstPool, firstCache, Planner{})
	firstListener := NewListener(firstPool, firstCache)
	secondListener := NewListener(secondPool, secondCache)
	secondListener.retryMin = 750 * time.Millisecond
	secondListener.retryMax = 750 * time.Millisecond
	reloadedBeforeReady := make(chan struct{})
	allowReady := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-allowReady:
		default:
			close(allowReady)
		}
	})
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
	pool, _, _ := newSchemaServiceTest(t, Planner{})
	listener := NewListener(pool, &Cache{})
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

func openSchemaListenerPool(t *testing.T, databaseURL string) *database.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), databaseURL, 5*time.Second)
	if err != nil {
		t.Fatalf("open listener pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitForListenerReady(t *testing.T, listener *Listener) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if listener.Ready(context.Background()) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener did not become ready")
}

func waitForListenerUnavailable(t *testing.T, listener *Listener) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if listener.Ready(context.Background()) != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("listener did not mark itself unavailable after connection loss")
}

func waitForCatalogVersion(t *testing.T, cache *Cache, version int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cache.Load().Version >= version {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("catalog did not reach version %d; current=%d", version, cache.Load().Version)
}
