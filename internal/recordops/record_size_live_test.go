package recordops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/relation"
)

// jsonStringOfSize renders a JSON string whose encoding alone is bytes long,
// so a record built from one field is predictably above or below the bound.
func jsonStringOfSize(bytes int) json.RawMessage {
	return json.RawMessage(`"` + strings.Repeat("x", bytes-2) + `"`)
}

// TestRecordSizeBoundSkipsOnlyThePatchPreImage pins the call-order contract
// hydrate depends on: create bounds its single hydration, update bounds only
// the second. Without the skip a record an older build stored above the bound
// could never be shrunk, because Service.Update hydrates the stored pre-image
// before it writes anything.
func TestRecordSizeBoundSkipsOnlyThePatchPreImage(t *testing.T) {
	oversized := func() map[string]any {
		return map[string]any{"payload": strings.Repeat("x", maximumRecordBytes)}
	}
	bounded := func() map[string]any { return map[string]any{"payload": "small"} }

	for _, test := range []struct {
		name     string
		mode     relation.InputMode
		records  []map[string]any
		wantFail []bool
	}{
		{
			name: "create bounds the inserted record",
			mode: relation.CreateInput, records: []map[string]any{oversized()},
			wantFail: []bool{true},
		},
		{
			name: "create accepts a bounded record",
			mode: relation.CreateInput, records: []map[string]any{bounded()},
			wantFail: []bool{false},
		},
		{
			name: "update tolerates an oversized pre-image and bounds the result",
			mode: relation.PatchInput, records: []map[string]any{oversized(), oversized()},
			wantFail: []bool{false, true},
		},
		{
			name: "update accepts shrinking an oversized pre-image",
			mode: relation.PatchInput, records: []map[string]any{oversized(), bounded()},
			wantFail: []bool{false, false},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutation := &relationMutation{mode: test.mode}
			for index, record := range test.records {
				// bindings is nil, so hydration returns before it reads the
				// transaction and only the size bound can fail.
				err := mutation.hydrate(context.Background(), nil, uuid.Nil, record)
				if test.wantFail[index] {
					assertQueryValidation(t, err, "record", "record_too_large")
					continue
				}
				if err != nil {
					t.Fatalf("hydrate call %d error = %v, want nil", index+1, err)
				}
			}
		})
	}
}
