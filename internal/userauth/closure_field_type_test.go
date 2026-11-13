package userauth

import (
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
)

func TestClosureAuthFieldTypeMatchesSchemaImport(t *testing.T) {
	for _, kind := range []schema.FieldType{"nonsense", "", schema.TypeText, schema.TypePassword, schema.TypeInteger} {
		_, _, ok := normalizeAuthFields([]schema.FieldInput{{Name: "custom", Type: kind, Required: true}}, nil)
		if ok != schema.IsAuthFieldType(kind) {
			t.Fatalf("kind=%q direct=%t imported=%t", kind, ok, schema.IsAuthFieldType(kind))
		}
	}
}
