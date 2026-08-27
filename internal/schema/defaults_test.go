package schema

import (
	"encoding/json"
	"testing"
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
		{name: "text quote", typ: TypeText, raw: `"O'Brien"`, want: "'O''Brien'::text"},
		{name: "timestamp", typ: TypeTimestampTZ, raw: `"2026-08-28T12:34:56+00:00"`, want: "'2026-08-28T12:34:56Z'::timestamptz"},
		{name: "uuid", typ: TypeUUID, raw: `"12345678-1234-4567-8901-123456789ABC"`, want: "'12345678-1234-4567-8901-123456789abc'::uuid"},
		{name: "jsonb", typ: TypeJSONB, raw: `{"quote":"O'Brien","items":[2,1]}`, want: "'{\"items\":[2,1],\"quote\":\"O''Brien\"}'::jsonb"},
		{name: "now", typ: TypeTimestampTZ, raw: `{"kind":"now"}`, want: "CURRENT_TIMESTAMP"},
		{name: "whitespace-normalized now", typ: TypeTimestampTZ, raw: "{ \n \t\"kind\" : \"now\" }", want: "CURRENT_TIMESTAMP"},
	}

	for _, tc := range cases {
		got, err := renderDefault(tc.typ, json.RawMessage(tc.raw))
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
		{name: "invalid uuid", typ: TypeUUID, raw: `"not-a-uuid"`},
		{name: "non-UTC timestamp", typ: TypeTimestampTZ, raw: `"2026-08-28T12:34:56+09:00"`},
		{name: "sql object", typ: TypeText, raw: `{"sql":"CURRENT_TIMESTAMP; DROP TABLE app.posts"}`},
		{name: "unknown timestamp object", typ: TypeTimestampTZ, raw: `{"sql":"now()"}`},
		{name: "timestamp object with SQL", typ: TypeTimestampTZ, raw: `{"kind":"now","sql":"now()"}`},
	}

	for _, tc := range cases {
		if _, err := renderDefault(tc.typ, json.RawMessage(tc.raw)); err == nil {
			t.Errorf("renderDefault(%s, %s) succeeded, want validation error", tc.typ, tc.raw)
		}
	}
}
