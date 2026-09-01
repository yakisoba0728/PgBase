package schema

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestDefaultRendersTypedLiterals(t *testing.T) {
	cases := []struct {
		name string
		typ  FieldType
		raw  string
		want string
	}{
		{name: "boolean", typ: TypeBoolean, raw: "true", want: "TRUE"},
		{name: "integer", typ: TypeInteger, raw: `"-42"`, want: "-42"},
		{name: "bigint", typ: TypeBigInt, raw: `"9223372036854775807"`, want: "9223372036854775807"},
		{name: "numeric", typ: TypeNumeric, raw: `"001.2300"`, want: "1.23::numeric"},
		{name: "text quote", typ: TypeText, raw: `"O'Brien"`, want: "E'O''Brien'::text"},
		{name: "timestamp", typ: TypeTimestampTZ, raw: `"2026-08-28T12:34:56+00:00"`, want: "E'2026-08-28T12:34:56Z'::timestamptz"},
		{name: "timestamp lower PostgreSQL boundary", typ: TypeTimestampTZ, raw: `"0001-01-01T00:00:00Z"`, want: "E'0001-01-01T00:00:00Z'::timestamptz"},
		{name: "timestamp upper PostgreSQL boundary", typ: TypeTimestampTZ, raw: `"9999-12-31T23:59:59.999999Z"`, want: "E'9999-12-31T23:59:59.999999Z'::timestamptz"},
		{name: "timestamp just below the microsecond rounding carry", typ: TypeTimestampTZ, raw: `"9999-12-31T23:59:59.9999994Z"`, want: "E'9999-12-31T23:59:59.9999994Z'::timestamptz"},
		{name: "text at the byte bound", typ: TypeText, raw: `"` + strings.Repeat("x", MaximumTextBytes) + `"`, want: "E'" + strings.Repeat("x", MaximumTextBytes) + "'::text"},
		{name: "uuid", typ: TypeUUID, raw: `"12345678-1234-4567-8901-123456789ABC"`, want: "E'12345678-1234-4567-8901-123456789abc'::uuid"},
		{name: "jsonb", typ: TypeJSONB, raw: `{"quote":"O'Brien","items":[2,1]}`, want: "E'{\"items\":[2,1],\"quote\":\"O''Brien\"}'::jsonb"},
		{name: "now", typ: TypeTimestampTZ, raw: `{"kind":"now"}`, want: "CURRENT_TIMESTAMP"},
		{name: "whitespace-normalized now", typ: TypeTimestampTZ, raw: "{ \n \t\"kind\" : \"now\" }", want: "CURRENT_TIMESTAMP"},
	}

	for _, tc := range cases {
		_, got, err := normalizeDefault(tc.typ, json.RawMessage(tc.raw))
		if err != nil || got != tc.want {
			t.Errorf("renderDefault(%s, %s) = %q, %v; want %q, nil", tc.typ, tc.raw, got, err, tc.want)
		}
	}
}

func TestDefaultRejectsUntypedAndSQLInputs(t *testing.T) {
	cases := []struct {
		name string
		typ  FieldType
		raw  string
	}{
		{name: "nan", typ: TypeNumeric, raw: `"NaN"`},
		{name: "infinity", typ: TypeNumeric, raw: `"Infinity"`},
		{name: "numeric JSON number", typ: TypeNumeric, raw: "1.2"},
		{name: "invalid timestamp", typ: TypeTimestampTZ, raw: `"not-a-timestamp"`},
		{name: "PostgreSQL has no year zero", typ: TypeTimestampTZ, raw: `"0000-01-01T00:00:00Z"`},
		{name: "microsecond rounding carries past year 9999", typ: TypeTimestampTZ, raw: `"9999-12-31T23:59:59.999999999Z"`},
		{name: "microsecond rounding tie carries past year 9999", typ: TypeTimestampTZ, raw: `"9999-12-31T23:59:59.9999995Z"`},
		{name: "text default over the byte bound", typ: TypeText, raw: `"` + strings.Repeat("x", MaximumTextBytes+1) + `"`},
		{name: "invalid uuid", typ: TypeUUID, raw: `"not-a-uuid"`},
		{name: "non-UTC timestamp", typ: TypeTimestampTZ, raw: `"2026-08-28T12:34:56+09:00"`},
		{name: "sql object", typ: TypeText, raw: `{"sql":"CURRENT_TIMESTAMP; DROP TABLE app.posts"}`},
		{name: "unknown timestamp object", typ: TypeTimestampTZ, raw: `{"sql":"now()"}`},
		{name: "timestamp object with SQL", typ: TypeTimestampTZ, raw: `{"kind":"now","sql":"now()"}`},
	}

	for _, tc := range cases {
		if _, _, err := normalizeDefault(tc.typ, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("renderDefault(%s, %s) succeeded, want validation error", tc.typ, tc.raw)
		}
	}
}

// TestStoredDefaultToleratesLegacyOversizedText pins the input/stored split.
// A database written before MaximumTextBytes reached defaults can hold a text
// default above the bound; the input path must keep refusing it while the
// catalog path must keep reading it, because every other bound stays shared.
func TestStoredDefaultToleratesLegacyOversizedText(t *testing.T) {
	legacy := json.RawMessage(`"` + strings.Repeat("x", MaximumTextBytes+1) + `"`)
	if _, _, err := normalizeDefault(TypeText, legacy); err == nil {
		t.Fatal("normalizeDefault accepted an oversized text default, want validation error")
	}
	canonical, rendered, err := normalizeStoredDefault(TypeText, legacy)
	if err != nil {
		t.Fatalf("normalizeStoredDefault(oversized text) error = %v, want nil", err)
	}
	if string(canonical) != string(legacy) {
		t.Fatalf("stored canonical default = %q, want the original value", canonical)
	}
	if rendered != "E'"+strings.Repeat("x", MaximumTextBytes+1)+"'::text" {
		t.Fatalf("stored rendered default = %q, want the escaped literal", rendered)
	}

	// Leniency is scoped to the text byte bound. Every other refusal is a
	// value no catalog can legally hold, so it must survive on both paths.
	for _, tc := range []struct {
		name string
		typ  FieldType
		raw  string
	}{
		{name: "nan", typ: TypeNumeric, raw: `"NaN"`},
		{name: "invalid uuid", typ: TypeUUID, raw: `"not-a-uuid"`},
		{name: "non-UTC timestamp", typ: TypeTimestampTZ, raw: `"2026-08-28T12:34:56+09:00"`},
		{name: "sql object", typ: TypeText, raw: `{"sql":"DROP TABLE app.posts"}`},
		{name: "text with NUL", typ: TypeText, raw: `"a\u0000b"`},
		{name: "unsupported type", typ: TypeRelation, raw: `"x"`},
	} {
		if _, _, err := normalizeStoredDefault(tc.typ, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("normalizeStoredDefault(%s) accepted %s, want validation error", tc.name, tc.raw)
		}
	}
}

func TestTextDefaultLiteralRoundTripsWithStandardConformingStringsDisabled(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "set standard_conforming_strings = off"); err != nil {
		t.Fatalf("disable standard_conforming_strings: %v", err)
	}

	want := `C:\temp\O'Brien`
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	_, rendered, err := normalizeDefault(TypeText, raw)
	if err != nil {
		t.Fatalf("render text default: %v", err)
	}
	var got string
	if err := connection.QueryRow(ctx, "select "+rendered).Scan(&got); err != nil {
		t.Fatalf("evaluate rendered default %q: %v", rendered, err)
	}
	if got != want {
		t.Fatalf("rendered default round trip = %q, want %q (SQL %q)", got, want, rendered)
	}
}
