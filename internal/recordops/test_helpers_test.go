package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
)

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
