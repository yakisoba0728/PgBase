package query

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestCompileInsertUsesCatalogOrdinalAndParametersForEveryValue(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	input := map[string]json.RawMessage{
		"metadata":    json.RawMessage(`{"attempt":"'); DROP TABLE app.records; --"}`),
		"title":       json.RawMessage(`"'); DROP TABLE app.records; --"`),
		"large_count": json.RawMessage(`"9223372036854775807"`),
	}

	got, err := CompileInsert(collection, input, id)
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	wantSQL := `INSERT INTO "app"."physical_records" ("id", "created_at", "updated_at", "title", "large_count", "metadata") VALUES ($1, statement_timestamp(), statement_timestamp(), $2, $3, $4) RETURNING "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note"`
	if got.SQL != wantSQL {
		t.Fatalf("SQL = %q, want %q", got.SQL, wantSQL)
	}
	if strings.Contains(got.SQL, "DROP TABLE") {
		t.Fatalf("SQL contains request value: %q", got.SQL)
	}
	wantArgs := []any{
		id,
		"'); DROP TABLE app.records; --",
		int64(9223372036854775807),
		json.RawMessage(`{"attempt":"'); DROP TABLE app.records; --"}`),
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", got.Args, wantArgs)
	}
}

func TestCompileInsertOmissionRulesAndJSONBNull(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	statement, err := CompileInsert(collection, map[string]json.RawMessage{
		"title":    json.RawMessage(`"required"`),
		"metadata": json.RawMessage(`null`),
	}, id)
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	wantSQL := `INSERT INTO "app"."physical_records" ("id", "created_at", "updated_at", "title", "metadata") VALUES ($1, statement_timestamp(), statement_timestamp(), $2, $3) RETURNING "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note"`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL = %q, want %q", statement.SQL, wantSQL)
	}
	if len(statement.Args) != 3 {
		t.Fatalf("Args length = %d, want 3", len(statement.Args))
	}
	assertJSONEqual(t, statement.Args[2], `null`)

	_, err = CompileInsert(collection, map[string]json.RawMessage{}, id)
	assertValidationDetail(t, err, "title", "required")
}

func TestCompileInsertProducesDefaultValuesStatementWhenCollectionHasNoFields(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	statement, err := CompileInsert(schema.Collection{TableName: "empty"}, nil, id)
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	wantSQL := `INSERT INTO "app"."empty" ("id", "created_at", "updated_at") VALUES ($1, statement_timestamp(), statement_timestamp()) RETURNING "id", "created_at", "updated_at"`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL = %q, want %q", statement.SQL, wantSQL)
	}
}

func TestCompileSelectByIDUsesCatalogIdentifiersAndParameter(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	got := CompileSelectByID(compilerCollection(), id)
	want := Statement{
		SQL:  `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" WHERE "id" = $1`,
		Args: []any{id},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CompileSelectByID() = %#v, want %#v", got, want)
	}
}

func TestCompileUpdateUsesOrdinalAssignmentsAndReturnsRecord(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	got, err := CompileUpdate(collection, id, map[string]json.RawMessage{
		"metadata":    json.RawMessage(`["safe",1]`),
		"large_count": json.RawMessage(`"-42"`),
		"title":       json.RawMessage(`"updated"`),
	})
	if err != nil {
		t.Fatalf("CompileUpdate() error = %v", err)
	}
	wantSQL := `UPDATE "app"."physical_records" SET "title" = $1, "large_count" = $2, "metadata" = $3, "updated_at" = statement_timestamp() WHERE "id" = $4 RETURNING "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note"`
	if got.SQL != wantSQL {
		t.Fatalf("SQL = %q, want %q", got.SQL, wantSQL)
	}
	wantArgs := []any{"updated", int64(-42), json.RawMessage(`["safe",1]`), id}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", got.Args, wantArgs)
	}
}

func TestCompileUpdateRejectsEmptyAndUnknownFieldsBeforeConstructingSQL(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	id := uuid.New()
	statement, err := CompileUpdate(collection, id, map[string]json.RawMessage{})
	if statement.SQL != "" || statement.Args != nil {
		t.Fatalf("empty patch statement = %#v, want zero statement", statement)
	}
	assertValidationDetail(t, err, "", "empty_patch")

	malicious := `title" = $1; DROP TABLE app.physical_records; --`
	statement, err = CompileUpdate(collection, id, map[string]json.RawMessage{malicious: json.RawMessage(`"x"`)})
	if statement.SQL != "" || statement.Args != nil {
		t.Fatalf("unknown field statement = %#v, want zero statement", statement)
	}
	assertValidationDetail(t, err, malicious, "unknown_field")
}

func TestCompileDeleteUsesOnlyCatalogTableAndIDParameter(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	got := CompileDelete(compilerCollection(), id)
	want := Statement{SQL: `DELETE FROM "app"."physical_records" WHERE "id" = $1`, Args: []any{id}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CompileDelete() = %#v, want %#v", got, want)
	}
}

func TestCompileListBuildsStableFirstAndNextPagesWithLimitPlusOne(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	first, err := CompileList(collection, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	wantFirst := Statement{
		SQL:  `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ORDER BY "created_at" ASC NULLS LAST, "id" ASC NULLS LAST LIMIT $1`,
		Args: []any{51},
	}
	if !reflect.DeepEqual(first, wantFirst) {
		t.Fatalf("CompileList(first) = %#v, want %#v", first, wantFirst)
	}

	after := &PageCursor{
		Version:   cursorVersion,
		Sort:      []SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}},
		Values:    []json.RawMessage{json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
		ExpiresAt: time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
	}
	next, err := CompileList(collection, ListQuery{After: after, Limit: 200})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	wantNext := Statement{
		SQL: `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ` +
			`WHERE (("created_at" > $1 OR ($1 IS NOT NULL AND "created_at" IS NULL)) ` +
			`OR (("created_at" IS NOT DISTINCT FROM $1) AND ("id" > $2 OR ($2 IS NOT NULL AND "id" IS NULL)))) ` +
			`ORDER BY "created_at" ASC NULLS LAST, "id" ASC NULLS LAST LIMIT $3`,
		Args: []any{
			time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC),
			uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
			201,
		},
	}
	if !reflect.DeepEqual(next, wantNext) {
		t.Fatalf("CompileList(next) =\n%#v\nwant\n%#v", next, wantNext)
	}
}

func TestCompileListNormalizesPageLimitBeforeAddingLookahead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "zero uses default", limit: 0, want: 51},
		{name: "negative uses default", limit: -1, want: 51},
		{name: "above maximum is capped", limit: 201, want: 201},
		{name: "maximum integer cannot overflow", limit: math.MaxInt, want: 201},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := CompileList(compilerCollection(), ListQuery{Limit: test.limit})
			if err != nil {
				t.Fatalf("CompileList() error = %v", err)
			}
			if len(first.Args) != 1 || first.Args[0] != test.want {
				t.Fatalf("CompileList(nil, %d) args = %#v, want [%d]", test.limit, first.Args, test.want)
			}

			after := &PageCursor{
				Version: cursorVersion,
				Sort:    []SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}},
				Values:  []json.RawMessage{json.RawMessage(`"2026-08-28T12:00:00Z"`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
			}
			next, err := CompileList(compilerCollection(), ListQuery{After: after, Limit: test.limit})
			if err != nil {
				t.Fatalf("CompileList() error = %v", err)
			}
			if len(next.Args) != 3 || next.Args[2] != test.want {
				t.Fatalf("CompileList(after, %d) args = %#v, want third arg %d", test.limit, next.Args, test.want)
			}
		})
	}
}

func TestCompileListCompilesFilterAndSortAgainstCatalog(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	statement, err := CompileList(collection, ListQuery{
		Filter: mustParseFilter(t, `(title = 'wanted' && large_count >= 10) || optional_note = null`),
		Sort:   []SortKey{{Field: "title", Desc: true}},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	wantSQL := `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ` +
		`WHERE ((("title" = $1) AND ("large_count" >= $2)) OR ("optional_note" IS NULL)) ` +
		`ORDER BY "title" DESC NULLS LAST, "id" ASC NULLS LAST LIMIT $3`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	if !reflect.DeepEqual(statement.Args, []any{"wanted", int64(10), 11}) {
		t.Fatalf("Args = %#v", statement.Args)
	}
}

func TestCompileListRejectsFilterSortAndCatalogMismatches(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	tests := []struct {
		name  string
		query ListQuery
		field string
		code  string
	}{
		{
			name:  "unknown filter field",
			query: ListQuery{Filter: mustParseFilter(t, `ghost = 'x'`)},
			field: "filter", code: "unknown_field",
		},
		{
			name:  "jsonb filter",
			query: ListQuery{Filter: mustParseFilter(t, `metadata = '{}'`)},
			field: "filter", code: "unsupported_type",
		},
		{
			name:  "type mismatch filter",
			query: ListQuery{Filter: mustParseFilter(t, `large_count = 'x'`)},
			field: "filter", code: "type_mismatch",
		},
		{
			name:  "number literal on text",
			query: ListQuery{Filter: mustParseFilter(t, `title > 5`)},
			field: "filter", code: "type_mismatch",
		},
		{
			name:  "number literal on timestamptz",
			query: ListQuery{Filter: mustParseFilter(t, `created_at > 5`)},
			field: "filter", code: "type_mismatch",
		},
		{
			name:  "unknown sort field",
			query: ListQuery{Sort: []SortKey{{Field: "ghost"}}},
			field: "sort", code: "unknown_field",
		},
		{
			name:  "jsonb sort",
			query: ListQuery{Sort: []SortKey{{Field: "metadata"}}},
			field: "sort", code: "unsupported_type",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := CompileList(collection, test.query)
			assertValidationDetail(t, err, test.field, test.code)
		})
	}
}

func TestCompileListFlipsReversedLiteralComparisons(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	statement, err := CompileList(collection, ListQuery{Filter: mustParseFilter(t, `10 > large_count || 'wanted' = title`)})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	wantSQL := `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ` +
		`WHERE (("large_count" < $1) OR ("title" = $2)) ORDER BY "created_at" ASC NULLS LAST, "id" ASC NULLS LAST LIMIT $3`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	if !reflect.DeepEqual(statement.Args, []any{int64(10), "wanted", 51}) {
		t.Fatalf("Args = %#v", statement.Args)
	}
}

func mustParseFilter(t *testing.T, expression string) filterNode {
	t.Helper()
	node, err := parseFilter(expression)
	if err != nil {
		t.Fatalf("parseFilter(%q) error = %v", expression, err)
	}
	return node
}

func TestCompilerQuotesCatalogDerivedPhysicalIdentifiers(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{
		TableName: `records"; DROP TABLE pgbase.administrators; --`,
		Fields: []schema.Field{{
			Name:       "safe_api_name",
			ColumnName: `column"; DROP TABLE pgbase.sessions; --`,
			Type:       schema.TypeText,
			Ordinal:    0,
			Required:   true,
		}},
	}
	statement, err := CompileInsert(collection, map[string]json.RawMessage{"safe_api_name": json.RawMessage(`"value"`)}, uuid.New())
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	if strings.Count(statement.SQL, "DROP TABLE") != 3 {
		t.Fatalf("quoted SQL = %q, want table once and returned column twice as quoted identifiers", statement.SQL)
	}
	if !strings.Contains(statement.SQL, `"app"."records""; DROP TABLE pgbase.administrators; --"`) ||
		!strings.Contains(statement.SQL, `"column""; DROP TABLE pgbase.sessions; --"`) {
		t.Fatalf("catalog identifiers were not sanitized: %q", statement.SQL)
	}
}

func TestCompileInsertRejectsUnsupportedCatalogFieldType(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{TableName: "records", Fields: []schema.Field{{Name: "cost", ColumnName: "cost", Type: schema.FieldType("money")}}}
	_, err := CompileInsert(collection, map[string]json.RawMessage{"cost": json.RawMessage(`"1.00"`)}, uuid.New())
	assertValidationDetail(t, err, "cost", "unsupported_type")
}

func TestCompileInsertKeepsNumericArgumentAsExactPgtypeNumeric(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{TableName: "records", Fields: []schema.Field{{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric, Required: true}}}
	statement, err := CompileInsert(collection, map[string]json.RawMessage{"amount": json.RawMessage(`"12345678901234567890.12345678901234567890"`)}, uuid.New())
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	numeric, ok := statement.Args[1].(pgtype.Numeric)
	if !ok {
		t.Fatalf("numeric arg type = %T, want pgtype.Numeric", statement.Args[1])
	}
	value, err := numeric.Value()
	if err != nil {
		t.Fatalf("numeric.Value() error = %v", err)
	}
	if value != "12345678901234567890.12345678901234567890" {
		t.Fatalf("numeric arg = %#v", value)
	}
}

func compilerCollection() schema.Collection {
	return schema.Collection{
		Name:      "records",
		TableName: "physical_records",
		Fields: []schema.Field{
			{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB, Ordinal: 2},
			{Name: "optional_note", ColumnName: "optional_note", Type: schema.TypeText, Ordinal: 3},
			{Name: "title", ColumnName: "title", Type: schema.TypeText, Required: true, Ordinal: 0},
			{Name: "large_count", ColumnName: "large_count", Type: schema.TypeBigInt, Required: true, Default: json.RawMessage(`"0"`), Ordinal: 1},
		},
	}
}

func assertValidationDetail(t *testing.T, err error, field, code string) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if len(validationErr.Details) != 1 || validationErr.Details[0] != (ValidationDetail{Field: field, Code: code}) {
		t.Fatalf("validation details = %#v, want field %q code %q", validationErr.Details, field, code)
	}
}
