package userauth

import (
	"encoding/json"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthProfileNumbersPreservePatchRuleSemantics(t *testing.T) {
	stored := map[string]any{"integer": json.Number("1.0"), "list": []any{json.Number("2")}}
	patch := map[string]any{"integer": float64(1), "list": []any{float64(2)}}
	if !authRuleValuesEqual(stored, patch) {
		t.Fatal("equal JSON numeric values were marked changed")
	}
	if authRuleValuesEqual(json.Number("9007199254740993"), json.Number("9007199254740992")) {
		t.Fatal("distinct large JSON numbers compared equal")
	}
	fields := []schema.FieldInput{{Name: "count", Type: schema.TypeInteger}}
	if !EvaluateAuthRecordRule("count = 3", fields, AuthRuleContext{Current: AuthRecord{Fields: map[string]any{"count": json.Number("3")}}}) {
		t.Fatal("existing scalar rule lost numeric profile support")
	}
}
