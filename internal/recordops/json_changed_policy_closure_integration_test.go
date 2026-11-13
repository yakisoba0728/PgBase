//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"testing"
)

func TestJSONChangedPolicyDistinguishesNumbersFromMarkerStrings(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	f.enable(t, "records")
	allow := accessrule.Rule{Op: "allow"}
	update, err := query.ParseAccessRule("@request.body.metadata:changed = false")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.Rules{List: allow, View: allow, Create: allow, Update: update, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	created, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "json-changed-create", Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"immutable"`), "metadata": json.RawMessage(`{"flag":1}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"flag":1}`, `{"flag":1.0}`, `{"flag":1e0}`} {
		if _, err := f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "json-changed-equal", Collection: "records", ID: created.InternalID, Input: map[string]json.RawMessage{"metadata": json.RawMessage(raw)}}); err != nil {
			t.Fatalf("semantic no-op %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"flag":"#number:1"}`, `{"flag":2}`} {
		if _, err := f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "json-changed-denied", Collection: "records", ID: created.InternalID, Input: map[string]json.RawMessage{"metadata": json.RawMessage(raw)}}); !errors.Is(err, query.ErrRecordNotFound) {
			t.Fatalf("mutation %s: %v", raw, err)
		}
	}
	var stored string
	if err := f.pool.QueryRow(ctx, `SELECT metadata->>'flag' FROM app.records WHERE id=$1`, created.InternalID).Scan(&stored); err != nil || stored != "1" {
		t.Fatalf("stored=%s err=%v", stored, err)
	}
}
