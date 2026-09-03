package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

func TestListenerOperationPanicIsRecordedAsFailureAndRepanics(t *testing.T) {
	recorder := &schemaTelemetryRecorder{}
	listener := &Listener{recorder: recorder}
	panicValue := errors.New("listener panic sentinel")
	recovered := func() (value any) {
		defer func() { value = recover() }()
		var operationErr error
		defer listener.finishOperation(context.Background(), observability.OperationListenerReload, time.Now(), &operationErr)
		panic(panicValue)
	}()
	if recovered != panicValue {
		t.Fatalf("recovered panic = %v, want original panic", recovered)
	}
	events := recorder.snapshot()
	if len(events) != 1 || events[0].Operation() != observability.OperationListenerReload || events[0].Outcome() != observability.OutcomeFailure {
		t.Fatalf("panic telemetry = %#v", events)
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
