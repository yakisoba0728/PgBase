package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
)

// recordOperationsTestTimeout bounds one integration test's database work.
// The slowest test takes about 30 seconds without the race detector, so a test
// still running at this deadline is hung on a lock, a pool connection or a
// barrier. Failing it here leaves the rest of the package's 20-minute go test
// budget, and the goroutine dump, to the tests that still work.
const recordOperationsTestTimeout = 3 * time.Minute

// recordOperationsPoolCloseTimeout bounds the cleanup wait for a fixture pool.
// pgxpool.Close waits for every checked-out connection, so a goroutine a test
// left parked while holding one would otherwise block cleanup forever.
const recordOperationsPoolCloseTimeout = 30 * time.Second

// recordOperationsTestContext is the context an integration test drives its
// operations with. It ends at the test deadline and, through t.Context, before
// the test's cleanups run, so a goroutine still waiting on PostgreSQL returns
// its pool connection before the fixture pool is closed.
func recordOperationsTestContext(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), recordOperationsTestTimeout)
	t.Cleanup(cancel)
	return ctx
}

// closePoolOnCleanup closes a fixture pool without letting a leaked connection
// hang the package. On timeout it reports every goroutine so the holder is
// named, and leaves the database cleanup to terminate the session.
func closePoolOnCleanup(t testing.TB, pool *database.Pool) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), recordOperationsPoolCloseTimeout)
		defer cancel()
		if err := pool.CloseContext(ctx); err != nil {
			stacks := make([]byte, 1<<20)
			stacks = stacks[:runtime.Stack(stacks, true)]
			t.Errorf("close fixture pool: a connection is still checked out after %s (%v)\n%s",
				recordOperationsPoolCloseTimeout, err, stacks)
		}
	})
}

// IsCode classifies a record operation failure. It lives here because no
// runtime caller needs it: the API layer maps recordops errors by their own
// switch, and only these tests assert on the code.
func IsCode(err error, code ErrorCode) bool {
	var operationError *Error
	return errors.As(err, &operationError) && operationError.Code == code
}

// jsonStringOfSize renders a JSON string whose encoding alone is bytes long,
// so a record built from one field is predictably above or below the bound.
func jsonStringOfSize(bytes int) json.RawMessage {
	return json.RawMessage(`"` + strings.Repeat("x", bytes-2) + `"`)
}

type relationTelemetryRecorder struct {
	mu     sync.Mutex
	events []observability.Event
}

func (r *relationTelemetryRecorder) Record(_ context.Context, event observability.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *relationTelemetryRecorder) snapshot() []observability.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Event(nil), r.events...)
}

func assertQueryValidation(t *testing.T, err error, field, code string) {
	t.Helper()
	var validation *query.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want query validation", err)
	}
	want := []query.ValidationDetail{{Field: field, Code: code}}
	if !reflect.DeepEqual(validation.Details, want) {
		t.Fatalf("validation = %#v, want %#v", validation.Details, want)
	}
}
