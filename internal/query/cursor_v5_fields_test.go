package query

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func fieldCursorCollection() schema.Collection {
	return schema.Collection{Name: "fields", Fields: []schema.Field{
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000001"), Name: "label", ColumnName: "label", Type: schema.TypeText},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000002"), Name: "expected", ColumnName: "expected", Type: schema.TypeText},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000003"), Name: "published", ColumnName: "published", Type: schema.TypeTimestampTZ},
	}}
}

func fieldCursorFingerprint(t *testing.T, collection schema.Collection, expression string) []byte {
	t.Helper()
	plan, _, err := planRelationFilter(schema.Catalog{}, collection, mustParseFilter(t, expression))
	if err != nil {
		t.Fatal(err)
	}
	value, err := cursorV5QueryFingerprint(cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef")), plan)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCursorV5OrdinaryLiteralAndFunctionGoldenUnchanged(t *testing.T) {
	for _, tc := range []struct{ expression, want string }{
		{`label ~ 'match'`, "427236e2aaf4c80b25371228e9acc8b7f15c620767d2440a287ac05744483117"},
		{`strftime('%Y', published) = '2026'`, "287c5f13b23b24a33fb82e7b8fd3ebafe3d2172a7e937cf7bb13c02108357658"},
	} {
		if got := hex.EncodeToString(fieldCursorFingerprint(t, fieldCursorCollection(), tc.expression)); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.expression, got, tc.want)
		}
	}
}

func TestCursorV5FieldFingerprintUsesBothStableTypedOperands(t *testing.T) {
	collection := fieldCursorCollection()
	original := fieldCursorFingerprint(t, collection, `label = expected`)
	for _, expression := range []string{`label = expected:lower`, `label:lower = expected`, `expected = label`, `label != expected`, `label = ''`} {
		if bytes.Equal(original, fieldCursorFingerprint(t, collection, expression)) {
			t.Errorf("changed comparison %q retained the same binding", expression)
		}
	}
	for _, index := range []int{0, 1} {
		changed := collection
		changed.Fields = append([]schema.Field(nil), collection.Fields...)
		changed.Fields[index].ID = uuid.New()
		if bytes.Equal(original, fieldCursorFingerprint(t, changed, `label = expected`)) {
			t.Errorf("changed operand %d ID retained binding", index)
		}
	}
	renamed := collection
	renamed.Fields = append([]schema.Field(nil), collection.Fields...)
	renamed.Fields[0].Name, renamed.Fields[0].ColumnName = "renamed_left", "physical_left"
	renamed.Fields[1].Name, renamed.Fields[1].ColumnName = "renamed_right", "physical_right"
	if !bytes.Equal(original, fieldCursorFingerprint(t, renamed, `renamed_left = renamed_right`)) {
		t.Fatal("renaming stable fields changed the binding")
	}
	for index := range collection.Fields[:2] {
		collection.Fields[index].Type = schema.TypeEditor
	}
	if bytes.Equal(original, fieldCursorFingerprint(t, collection, `label = expected`)) {
		t.Fatal("changed operand types retained binding")
	}
}

func TestFieldComparisonPlanningRejectsUnboundOperands(t *testing.T) {
	collection := fieldCursorCollection()
	collection.Fields = append(collection.Fields, schema.Field{ID: uuid.New(), Name: "amount", Type: schema.TypeInteger})
	for _, tc := range []struct{ expression, detail string }{
		{`label = published`, "type_mismatch"},
		{`amount = amount:lower`, "unsupported_modifier"},
		{`label = owner.name`, "unknown_field"},
	} {
		_, _, err := planRelationFilter(schema.Catalog{}, collection, mustParseFilter(t, tc.expression))
		assertValidationDetail(t, err, "filter", tc.detail)
	}
	catalog, posts := relationFilterSortCatalog()
	for _, expression := range []string{`owner.name = id`, `owner.name = owner.name`} {
		_, _, err := planRelationFilter(catalog, posts, mustParseFilter(t, expression))
		assertValidationDetail(t, err, "filter", "relation_filter_unsupported")
	}
}
