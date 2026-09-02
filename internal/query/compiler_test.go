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

	after := &pageBoundary{
		Values: []json.RawMessage{json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
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

			after := &pageBoundary{
				Values: []json.RawMessage{json.RawMessage(`"2026-08-28T12:00:00Z"`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
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
		`ORDER BY "title" DESC NULLS FIRST, "id" DESC NULLS FIRST LIMIT $3`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	if !reflect.DeepEqual(statement.Args, []any{"wanted", int64(10), 11}) {
		t.Fatalf("Args = %#v", statement.Args)
	}
}

func TestCompileListOrdersDescendingPagesAlongOneIndexDirection(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	after := &pageBoundary{
		Values: []json.RawMessage{json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
	}
	statement, err := CompileList(collection, ListQuery{
		Sort: []SortKey{{Field: systemFieldCreatedAt, Desc: true}}, After: after, Limit: 50,
	})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	// Both keys are "not null" columns, so the descending page is spelled with
	// the btree default NULLS FIRST and the appended tiebreaker follows the key
	// it breaks ties for. A mixed "DESC NULLS FIRST, ASC NULLS LAST" order can
	// use the "(created_at, id)" index in neither direction.
	wantSQL := `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ` +
		`WHERE (("created_at" < $1 OR ($1 IS NULL AND "created_at" IS NOT NULL)) ` +
		`OR (("created_at" IS NOT DISTINCT FROM $1) AND ("id" < $2 OR ($2 IS NULL AND "id" IS NOT NULL)))) ` +
		`ORDER BY "created_at" DESC NULLS FIRST, "id" DESC NULLS FIRST LIMIT $3`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	wantArgs := []any{
		time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC),
		uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		51,
	}
	if !reflect.DeepEqual(statement.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", statement.Args, wantArgs)
	}
}

func TestCompileListKeepsNullsLastForNullableSortColumnsAndExplicitTiebreakers(t *testing.T) {
	t.Parallel()

	// "note" is indexed but not required, so it can hold NULL and keeps the
	// documented NULLS LAST placement in both directions.
	collection := schema.Collection{
		Name:      "records",
		TableName: "physical_records",
		Fields:    []schema.Field{{Name: "note", ColumnName: "note", Type: schema.TypeText, Indexed: true}},
	}
	order, err := resolveOrderSpec(collection, effectiveSortKeys([]SortKey{{Field: "note", Desc: true}}))
	if err != nil || order.terms[0].indexHint != orderIndexRoot {
		t.Fatalf("scalar order index hint = %v, err=%v", order.terms[0].indexHint, err)
	}
	after := &pageBoundary{
		Values: []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`)},
	}
	statement, err := CompileList(collection, ListQuery{
		Sort: []SortKey{{Field: "note", Desc: true}}, After: after, Limit: 10,
	})
	if err != nil {
		t.Fatalf("CompileList() error = %v", err)
	}
	wantSQL := `SELECT "id", "created_at", "updated_at", "note" FROM "app"."physical_records" ` +
		`WHERE (("note" < $1 OR ($1 IS NOT NULL AND "note" IS NULL)) ` +
		`OR (("note" IS NOT DISTINCT FROM $1) AND ("id" < $2 OR ($2 IS NULL AND "id" IS NOT NULL)))) ` +
		`ORDER BY "note" DESC NULLS LAST, "id" DESC NULLS FIRST LIMIT $3`
	if statement.SQL != wantSQL {
		t.Fatalf("SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	if statement.Args[0] != nil {
		t.Fatalf("null cursor value decoded to %#v, want untyped nil", statement.Args[0])
	}

	// A caller that spells out its own trailing "id" key keeps the direction it
	// asked for; only the tiebreaker the compiler appends is realigned.
	explicit, err := CompileList(collection, ListQuery{
		Sort:  []SortKey{{Field: "note", Desc: true}, {Field: systemFieldID}},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("CompileList(explicit tiebreaker) error = %v", err)
	}
	wantExplicit := `SELECT "id", "created_at", "updated_at", "note" FROM "app"."physical_records" ` +
		`ORDER BY "note" DESC NULLS LAST, "id" ASC NULLS LAST LIMIT $1`
	if explicit.SQL != wantExplicit {
		t.Fatalf("SQL =\n%q\nwant\n%q", explicit.SQL, wantExplicit)
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
		{
			name:  "unindexed sort",
			query: ListQuery{Sort: []SortKey{{Field: "optional_note"}}},
			field: "sort", code: "unindexed_field",
		},
		{
			name:  "unindexed updated timestamp sort",
			query: ListQuery{Sort: []SortKey{{Field: systemFieldUpdatedAt}}},
			field: "sort", code: "unindexed_field",
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

func TestParseSortRejectsOverflowAndDuplicates(t *testing.T) {
	t.Parallel()

	tooMany := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	_, err := parseSort(strings.Join(tooMany, ","))
	assertValidationDetail(t, err, "sort", "invalid_sort")

	_, err = parseSort("title,-title")
	assertValidationDetail(t, err, "sort", "invalid_sort")

	_, err = parseFilter(`title = '` + strings.Repeat("x", 5000) + `'`)
	assertValidationDetail(t, err, "filter", "filter_too_complex")
}

func TestParseFilterEnforcesNodeBudgetBelowByteLimit(t *testing.T) {
	t.Parallel()

	comparison := `title = 'wanted'`
	atLimit := strings.Repeat("(", maximumFilterNodes-1) + comparison + strings.Repeat(")", maximumFilterNodes-1)
	if len(atLimit) >= maximumFilterBytes {
		t.Fatalf("boundary fixture length = %d, want below %d-byte limit", len(atLimit), maximumFilterBytes)
	}
	if _, err := parseFilter(atLimit); err != nil {
		t.Fatalf("parseFilter() at %d nodes error = %v", maximumFilterNodes, err)
	}

	overLimit := "(" + atLimit + ")"
	_, err := parseFilter(overLimit)
	assertValidationDetail(t, err, "filter", "filter_too_complex")
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
			{Name: "title", ColumnName: "title", Type: schema.TypeText, Required: true, Indexed: true, Ordinal: 0},
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

// TestEverySortEntryPointSharesTheTiebreakerDirection pins tie ordering at the
// single place that decides it. The scalar page compiler, the relation-aware
// page, the root page and expansion each derive their sort keys from
// effectiveSortKeys; aligning the appended tiebreaker inside one caller instead
// would make the same sort produce a different tie order depending on whether a
// request happened to touch a relation, and a cursor issued by one path would
// then anchor against the other path's ordering.
func TestEverySortEntryPointSharesTheTiebreakerDirection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		requested []SortKey
		want      []SortKey
	}{
		{
			name:      "descending key carries its appended tiebreaker down",
			requested: []SortKey{{Field: "created_at", Desc: true}},
			want:      []SortKey{{Field: "created_at", Desc: true}, {Field: systemFieldID, Desc: true}},
		},
		{
			name:      "ascending key keeps an ascending tiebreaker",
			requested: []SortKey{{Field: "created_at"}},
			want:      []SortKey{{Field: "created_at"}, {Field: systemFieldID}},
		},
		{
			name:      "only the last key decides the appended direction",
			requested: []SortKey{{Field: "title", Desc: true}, {Field: "rank"}},
			want:      []SortKey{{Field: "title", Desc: true}, {Field: "rank"}, {Field: systemFieldID}},
		},
		{
			name:      "an explicit trailing tiebreaker is left alone",
			requested: []SortKey{{Field: "created_at", Desc: true}, {Field: systemFieldID}},
			want:      []SortKey{{Field: "created_at", Desc: true}, {Field: systemFieldID}},
		},
		{
			name:      "the default page order is unchanged",
			requested: nil,
			want:      []SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := effectiveSortKeys(test.requested)
			if len(got) != len(test.want) {
				t.Fatalf("effectiveSortKeys(%v) = %v, want %v", test.requested, got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("effectiveSortKeys(%v)[%d] = %+v, want %+v", test.requested, index, got[index], test.want[index])
				}
			}
		})
	}
}

// A boundary carrying a membership high-water and no keyset values is the legal
// first page of a v5 traversal. A boundary carrying no values for any other
// reason is a bug, and both page compilers must surface it as an invalid cursor
// rather than silently emit the unbounded page 1 statement a second time.
func TestPageStatementsRejectKeysetlessBoundariesOutsideTheV5FirstPage(t *testing.T) {
	t.Parallel()

	collection := compilerCollection()
	highWater := int64(7)
	firstPage, err := CompileList(collection, ListQuery{
		After: &pageBoundary{membership: &highWater, membershipOnly: true}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("CompileList(v5 first page) error = %v", err)
	}
	if !strings.Contains(firstPage.SQL, "pgbase_meta.record_creation_ordinals") ||
		strings.Contains(firstPage.SQL, "IS NOT DISTINCT FROM") {
		t.Fatalf("v5 first page SQL = %q, want a membership predicate and no keyset", firstPage.SQL)
	}

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("owner.name")
	if err != nil {
		t.Fatal(err)
	}
	relationPlan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatalf("compileRelationListPlan() error = %v", err)
	}
	for _, test := range []struct {
		name  string
		after *pageBoundary
	}{
		{name: "zero boundary", after: &pageBoundary{}},
		{name: "membership without the first-page marker", after: &pageBoundary{membership: &highWater}},
		{name: "empty value list", after: &pageBoundary{Values: []json.RawMessage{}, membership: &highWater}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := CompileList(collection, ListQuery{After: test.after, Limit: 10}); !IsInvalidCursor(err) {
				t.Fatalf("CompileList(%s) error = %v, want an invalid cursor", test.name, err)
			}
			if _, err := relationPlan.statement(test.after, 10); !IsInvalidCursor(err) {
				t.Fatalf("relation pageStatement(%s) error = %v, want an invalid cursor", test.name, err)
			}
		})
	}
}
