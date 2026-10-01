package query

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestCursorV5FunctionFingerprintUsesStableTypedOperands(t *testing.T) {
	collection := schema.Collection{Name: "function_records", Fields: []schema.Field{
		{ID: uuid.New(), Name: "longitude", ColumnName: "longitude", Type: schema.TypeInteger},
		{ID: uuid.New(), Name: "published", ColumnName: "published", Type: schema.TypeTimestampTZ},
		{ID: uuid.New(), Name: "place", ColumnName: "place", Type: schema.TypeGeoPoint},
	}}
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	fingerprint := func(collection schema.Collection, expression string, compatible bool) []byte {
		t.Helper()
		node := mustParseFilter(t, expression)
		if compatible {
			node = markPocketBaseLike(node)
		}
		plan, _, err := planRelationFilter(schema.Catalog{}, collection, node)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := cursorV5QueryFingerprint(codec, plan)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	const expression = `geoDistance(longitude, 2, 1, 2) < 1`
	original := fingerprint(collection, expression, false)
	renamed := collection
	renamed.Fields = append([]schema.Field(nil), collection.Fields...)
	renamed.Fields[0].Name, renamed.Fields[0].ColumnName = "renamed", "physical_renamed"
	if !bytes.Equal(original, fingerprint(renamed, `geoDistance(renamed, 2, 1, 2) < 1`, false)) {
		t.Fatal("renaming a field changed its stable function binding")
	}
	replaced := collection
	replaced.Fields = append([]schema.Field(nil), collection.Fields...)
	replaced.Fields[0].ID = uuid.New()
	if bytes.Equal(original, fingerprint(replaced, expression, false)) {
		t.Fatal("replacement field reused the original function binding")
	}
	replaced.Fields[0] = collection.Fields[0]
	replaced.Fields[0].Type = schema.TypeBigInt
	if bytes.Equal(original, fingerprint(replaced, expression, false)) {
		t.Fatal("changed field type reused the original function binding")
	}
	left := `strftime('%Y', published) = '2026' && geoDistance(place.lon, place.lat, 1, 2) < 1`
	right := `geoDistance(place.lon, place.lat, 1, 2) < 1 && strftime('%Y', published) = '2026'`
	if !bytes.Equal(fingerprint(collection, left, false), fingerprint(collection, right, false)) {
		t.Fatal("commutative group order changed the function binding")
	}
	like := `strftime('%Y', published) ~ '20%'`
	if bytes.Equal(fingerprint(collection, like, false), fingerprint(collection, like, true)) {
		t.Fatal("different LIKE semantics reused the function binding")
	}
	for _, expression := range []string{`strftime('%Y') = '2026'`, `strftime('%Y', '2026-09-22') = '2026'`} {
		fingerprint(collection, expression, false)
	}
}

func TestFunctionPlanRejectsInvalidOperandsBeforeCursorBinding(t *testing.T) {
	collection := compilerCollection()
	collection.Fields = append(collection.Fields, schema.Field{ID: uuid.New(), Name: "published", Type: schema.TypeTimestampTZ})
	for _, tc := range []struct{ expression, code string }{
		{`geoDistance(1, 2) < 1`, "invalid_filter"},
		{`geoDistance(title, 2, 1, 2) < 1`, "type_mismatch"},
		{`geoDistance(large_count, 2, 1, 2) < 'bad'`, "type_mismatch"},
		{`strftime('%Y', title) = '2026'`, "type_mismatch"},
		{`strftime('%Y', published, 'start of year') = '2026'`, "unsupported_modifier"},
		{`strftime(title, published) = '2026'`, "invalid_filter"},
		{`strftime('%Y', strftime('%Y', published)) = '2026'`, "invalid_filter"},
		{`strftime('%Y', published:lower) = '2026'`, "invalid_filter"},
		{`strftime('%Y', owner.published) = '2026'`, "unknown_field"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			_, _, err := planRelationFilter(schema.Catalog{}, collection, mustParseFilter(t, tc.expression))
			assertValidationDetail(t, err, "filter", tc.code)
		})
	}
}

func TestFunctionComparisonsRejectUnsupportedOperandShapes(t *testing.T) {
	for _, expression := range []string{
		`strftime('%Y', published) = title`,
		`title = strftime('%Y', published)`,
		`strftime('%Y', published) = strftime('%Y', published)`,
		`'2026' = strftime('%Y', published)`,
		`strftime('%Y', published):lower = '2026'`,
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := parseFilterRequest(expression)
			assertValidationDetail(t, err, "filter", "invalid_filter")
		})
	}
}
