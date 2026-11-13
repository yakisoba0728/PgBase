package accessrule

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
)

func TestCatalogPolicyRejectsInvalidLiteralTypes(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "items", TableName: "items", Fields: []schema.Field{{Name: "count", ColumnName: "count", Type: schema.TypeInteger}, {Name: "active", ColumnName: "active", Type: schema.TypeBoolean}, {Name: "title", ColumnName: "title", Type: schema.TypeText}}}
	context := PolicyContext{Catalog: schema.Catalog{Collections: map[string]schema.Collection{"items": collection}}, Collection: collection, Alias: "items"}
	for _, tc := range []struct {
		field, op, value string
		valid            bool
	}{
		{"count", "contains", "1", false}, {"active", "contains", "true", false}, {"id", "eq", `"not-a-uuid"`, false}, {"created_at", "eq", `"not-a-time"`, false},
		{"title", "contains", `"needle"`, true}, {"count", "eq", "1", true}, {"active", "eq", "true", true}, {"id", "eq", `"00000000-0000-0000-0000-000000000001"`, true}, {"created_at", "eq", `"2026-01-01T00:00:00Z"`, true},
	} {
		rule := Rule{Binding: BindingCatalog, Field: tc.field, Op: tc.op, Value: json.RawMessage(tc.value)}
		if err := ValidatePolicy(rule, context); (err == nil) != tc.valid {
			t.Errorf("%s %s %s valid=%v err=%v", tc.field, tc.op, tc.value, tc.valid, err)
		}
	}
}
