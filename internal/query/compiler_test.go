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
	first := CompileList(collection, nil, 50)
	wantFirst := Statement{
		SQL:  `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" ORDER BY "created_at" ASC, "id" ASC LIMIT $1`,
		Args: []any{51},
	}
	if !reflect.DeepEqual(first, wantFirst) {
		t.Fatalf("CompileList(first) = %#v, want %#v", first, wantFirst)
	}

	after := &PageCursor{
		Version:   1,
		CreatedAt: time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC),
		ID:        uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
	}
	next := CompileList(collection, after, 200)
	wantNext := Statement{
		SQL:  `SELECT "id", "created_at", "updated_at", "title", "large_count", "metadata", "optional_note" FROM "app"."physical_records" WHERE ("created_at", "id") > ($1, $2) ORDER BY "created_at" ASC, "id" ASC LIMIT $3`,
		Args: []any{after.CreatedAt, after.ID, 201},
	}
	if !reflect.DeepEqual(next, wantNext) {
		t.Fatalf("CompileList(next) = %#v, want %#v", next, wantNext)
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
			first := CompileList(compilerCollection(), nil, test.limit)
			if len(first.Args) != 1 || first.Args[0] != test.want {
				t.Fatalf("CompileList(nil, %d) args = %#v, want [%d]", test.limit, first.Args, test.want)
			}

			after := &PageCursor{Version: 1, CreatedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), ID: uuid.New()}
			next := CompileList(compilerCollection(), after, test.limit)
			if len(next.Args) != 3 || next.Args[2] != test.want {
				t.Fatalf("CompileList(after, %d) args = %#v, want third arg %d", test.limit, next.Args, test.want)
			}
		})
	}
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
