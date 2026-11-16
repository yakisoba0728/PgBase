//go:build integration

package schema

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown(t *testing.T) {
	ctx := context.Background()
	parsed, err := url.Parse(testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "4")
	parsed.RawQuery = query.Encode()
	databaseURL := parsed.String()
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	cache := &Cache{}
	listener := NewListener(pool, cache, nil, NewService(pool, cache, nil))
	listener.retryMin, listener.retryMax = 200*time.Millisecond, 200*time.Millisecond
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- listener.Run(runCtx) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("listener stop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("listener did not stop after cancellation")
			}
		})
	}
	t.Cleanup(stop)
	waitForListenerReady(t, listener)
	firstPID := listener.pid.Load()

	assertCapacity := func() {
		t.Helper()
		before := pool.Stats()
		if before.MaxConns != 4 || before.AcquiredConns != 0 {
			t.Errorf("idle listener pool max=%d acquired=%d, want 4 and 0", before.MaxConns, before.AcquiredConns)
		}
		type acquisition struct {
			connection *pgxpool.Conn
			err        error
		}
		results := make(chan acquisition, 4)
		acquireCtx, cancelAcquire := context.WithTimeout(ctx, time.Second)
		defer cancelAcquire()
		for range 4 {
			go func() {
				connection, err := pool.Acquire(acquireCtx)
				results <- acquisition{connection: connection, err: err}
			}()
		}
		var connections []*pgxpool.Conn
		for range 4 {
			result := <-results
			if result.err != nil {
				t.Errorf("concurrent request acquisition: %v", result.err)
				continue
			}
			connections = append(connections, result.connection)
			defer result.connection.Release()
			if result.connection.Conn().PgConn().PID() == listener.pid.Load() {
				t.Error("listener connection was returned to request pool")
			}
		}
		if len(connections) != 4 {
			t.Errorf("simultaneously available request connections=%d, want 4", len(connections))
		}
		if len(connections) == 0 {
			return
		}
		var backends, listenerWriterLocks int
		var applicationName, fsync, synchronousCommit, fullPageWrites string
		err := connections[0].QueryRow(ctx, `SELECT
			(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend'),
			(SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND mode='ShareLock' AND granted),
			(SELECT application_name FROM pg_stat_activity WHERE pid=$1),
			current_setting('fsync'), current_setting('synchronous_commit'), current_setting('full_page_writes')`, listener.pid.Load()).Scan(
			&backends, &listenerWriterLocks, &applicationName, &fsync, &synchronousCommit, &fullPageWrites)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("request_pool_max=%d held_requests=%d total_fixture_backends=%d listener_writer_locks=%d fsync=%s synchronous_commit=%s full_page_writes=%s", before.MaxConns, len(connections), backends, listenerWriterLocks, fsync, synchronousCommit, fullPageWrites)
		if backends != 5 {
			t.Errorf("fixture backends=%d, want four requests plus one listener", backends)
		}
		if listenerWriterLocks == 0 || applicationName != "pgbase" {
			t.Errorf("listener session lost initialization: writer_locks=%d application_name=%q", listenerWriterLocks, applicationName)
		}
		if err := listener.Ready(ctx); err != nil {
			t.Errorf("listener unready while four request slots are held: %v", err)
		}
	}
	assertCapacity()

	var terminated bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, firstPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate listener pid=%d: terminated=%v err=%v", firstPID, terminated, err)
	}
	waitForListenerUnavailable(t, listener)
	waitForListenerReady(t, listener)
	secondPID := listener.pid.Load()
	if secondPID == 0 || secondPID == firstPID {
		t.Fatalf("reconnect pid=%d, old pid=%d", secondPID, firstPID)
	}
	assertCapacity()
	stop()
	if err := listener.Ready(ctx); err == nil || listener.pid.Load() != 0 {
		t.Error("stopped listener retained readiness or backend identity")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var remaining int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid=$1 OR pid=$2`, firstPID, secondPID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener backends still present after reconnect and shutdown: %d", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pool.Close()
	deadline = time.Now().Add(2 * time.Second)
	for {
		gate, err := database.TryBackupGate(ctx, databaseURL, true, false)
		if err == nil {
			if err := gate.Close(ctx); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(err, database.ErrBackupGateBusy) || time.Now().After(deadline) {
			t.Fatalf("exclusive backup gate unavailable after listener and pool stop: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
