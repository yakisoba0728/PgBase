package migrate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestMigrationOperationPanicIsRecordedAsFailureAndRepanics(t *testing.T) {
	recorder := &migrationTelemetryRecorder{}
	runner := &Runner{recorder: recorder}
	panicValue := errors.New("migration panic sentinel")
	recovered := func() (value any) {
		defer func() { value = recover() }()
		var operationErr error
		defer runner.finishOperation(context.Background(), observability.OperationMigrationUp, time.Now(), &operationErr)
		panic(panicValue)
	}()
	if recovered != panicValue {
		t.Fatalf("recovered panic = %v, want original panic", recovered)
	}
	events := recorder.snapshot()
	if len(events) != 1 || events[0].Operation() != observability.OperationMigrationUp || events[0].Outcome() != observability.OutcomeFailure {
		t.Fatalf("panic telemetry = %#v", events)
	}
}

func latestSystemMigrationVersion(t *testing.T) int64 {
	t.Helper()
	migrations := system.Migrations()
	if len(migrations) == 0 {
		t.Fatal("embedded system migrations are empty")
	}
	return migrations[len(migrations)-1].Version
}

type migrationTelemetryRecorder struct {
	mu     sync.Mutex
	events []observability.Event
}

func (r *migrationTelemetryRecorder) Record(_ context.Context, event observability.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *migrationTelemetryRecorder) snapshot() []observability.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Event(nil), r.events...)
}
