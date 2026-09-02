package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
)

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
