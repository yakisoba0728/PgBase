package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestDecodeInputAcceptsEveryFieldTypeWithoutPrecisionLoss(t *testing.T) {
	t.Parallel()

	collection := allTypesCollection()
	input := map[string]json.RawMessage{
		"active":       json.RawMessage(`true`),
		"count":        json.RawMessage(`-2147483648`),
		"large_count":  json.RawMessage(`"9223372036854775807"`),
		"amount":       json.RawMessage(`"12345678901234567890.12345678901234567890"`),
		"title":        json.RawMessage(`"hello"`),
		"published_at": json.RawMessage(`"2026-08-28T21:34:56.123456789+09:00"`),
		"external_id":  json.RawMessage(`"12345678-1234-4567-8901-123456789ABC"`),
		"metadata":     json.RawMessage(` {"items":[1,2],"ok":true} `),
	}

	got, err := decodeInput(collection, input)
	if err != nil {
		t.Fatalf("decodeInput() error = %v", err)
	}

	assertValue(t, got["active"], true)
	assertValue(t, got["count"], int32(-2147483648))
	assertValue(t, got["large_count"], int64(9223372036854775807))
	assertNumeric(t, got["amount"], "12345678901234567890.12345678901234567890")
	assertValue(t, got["title"], "hello")
	assertValue(t, got["published_at"], time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC))
	assertValue(t, got["external_id"], uuid.MustParse("12345678-1234-4567-8901-123456789abc"))
	assertJSONEqual(t, got["metadata"], `{"items":[1,2],"ok":true}`)
}

func TestDecodeInputPreservesEveryValidJSONBShape(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{Fields: []schema.Field{{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB}}}
	for _, raw := range []string{`{"a":1}`, `[1,"two",false]`, `"scalar"`, `123.45678901234567890`, `true`, `null`} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			got, err := decodeInput(collection, map[string]json.RawMessage{"metadata": json.RawMessage(raw)})
			if err != nil {
				t.Fatalf("decodeInput() error = %v", err)
			}
			assertJSONEqual(t, got["metadata"], raw)
		})
	}
}

func TestDecodeInputAcceptsNullForOptionalNonJSONBFields(t *testing.T) {
	t.Parallel()

	input := make(map[string]json.RawMessage)
	for _, field := range allTypesCollection().Fields {
		if field.Type != schema.TypeJSONB {
			input[field.Name] = json.RawMessage(`null`)
		}
	}

	got, err := decodeInput(allTypesCollection(), input)
	if err != nil {
		t.Fatalf("decodeInput() error = %v", err)
	}
	for name := range input {
		if got[name] != nil {
			t.Errorf("decoded %s = %#v, want nil", name, got[name])
		}
	}
}

func TestDecodeInputRejectsNullForRequiredNonJSONBFields(t *testing.T) {
	t.Parallel()

	for _, fieldType := range []schema.FieldType{
		schema.TypeBoolean,
		schema.TypeInteger,
		schema.TypeBigInt,
		schema.TypeNumeric,
		schema.TypeText,
		schema.TypeTimestampTZ,
		schema.TypeUUID,
	} {
		fieldType := fieldType
		t.Run(string(fieldType), func(t *testing.T) {
			t.Parallel()
			collection := schema.Collection{Fields: []schema.Field{{Name: "required_value", Type: fieldType, Required: true}}}
			_, err := decodeInput(collection, map[string]json.RawMessage{"required_value": json.RawMessage(`null`)})
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("decodeInput() error = %v, want *ValidationError", err)
			}
			want := []ValidationDetail{{Field: "required_value", Code: "required"}}
			if !reflect.DeepEqual(validationErr.Details, want) {
				t.Fatalf("validation details = %#v, want %#v", validationErr.Details, want)
			}
		})
	}
}

func TestDecodeInputAcceptsJSONNullForRequiredJSONBField(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{Fields: []schema.Field{{Name: "metadata", Type: schema.TypeJSONB, Required: true}}}
	got, err := decodeInput(collection, map[string]json.RawMessage{"metadata": json.RawMessage(`null`)})
	if err != nil {
		t.Fatalf("decodeInput() error = %v", err)
	}
	assertJSONEqual(t, got["metadata"], `null`)
}

func TestDecodeInputRejectsUnknownFieldsAndTypeMismatches(t *testing.T) {
	t.Parallel()

	collection := allTypesCollection()
	tests := []struct {
		name  string
		field string
		raw   string
	}{
		{name: "unknown field", field: "title; DROP TABLE app.posts; --", raw: `"x"`},
		{name: "boolean string", field: "active", raw: `"true"`},
		{name: "integer string", field: "count", raw: `"1"`},
		{name: "integer fraction", field: "count", raw: `1.5`},
		{name: "integer overflow", field: "count", raw: `2147483648`},
		{name: "bigint number", field: "large_count", raw: `9223372036854775807`},
		{name: "bigint overflow", field: "large_count", raw: `"9223372036854775808"`},
		{name: "numeric number", field: "amount", raw: `1.2`},
		{name: "numeric nan", field: "amount", raw: `"NaN"`},
		{name: "numeric infinity", field: "amount", raw: `"Infinity"`},
		{name: "numeric precision beyond PostgreSQL", field: "amount", raw: `"` + strings.Repeat("9", 131073) + `"`},
		{name: "text object", field: "title", raw: `{}`},
		{name: "timestamp without offset", field: "published_at", raw: `"2026-08-28T12:34:56"`},
		{name: "invalid timestamp", field: "published_at", raw: `"tomorrow"`},
		{name: "invalid uuid", field: "external_id", raw: `"not-a-uuid"`},
		{name: "trailing json", field: "metadata", raw: `{}[]`},
		{name: "jsonb exponent beyond PostgreSQL", field: "metadata", raw: `{"value":1e131072}`},
		{name: "jsonb NUL", field: "metadata", raw: `{"value":"\u0000"}`},
		{name: "jsonb unpaired surrogate", field: "metadata", raw: `{"value":"\ud800"}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeInput(collection, map[string]json.RawMessage{test.field: json.RawMessage(test.raw)})
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("decodeInput() error = %v, want *ValidationError", err)
			}
			if len(validationErr.Details) != 1 || validationErr.Details[0].Field != test.field {
				t.Fatalf("validation details = %#v, want field %q", validationErr.Details, test.field)
			}
		})
	}
}

func TestDecodeInputBoundsIndexedTextBeforePostgreSQLBtreeLimits(t *testing.T) {
	t.Parallel()

	for _, field := range []schema.Field{
		{Name: "indexed", Type: schema.TypeText, Indexed: true},
		{Name: "unique", Type: schema.TypeText, Unique: true},
	} {
		collection := schema.Collection{Fields: []schema.Field{field}}
		atLimit := json.RawMessage(strconv.Quote(strings.Repeat("x", maximumIndexedTextBytes)))
		if _, err := decodeInput(collection, map[string]json.RawMessage{field.Name: atLimit}); err != nil {
			t.Fatalf("decodeInput(%s at limit) error = %v", field.Name, err)
		}

		overLimit := json.RawMessage(strconv.Quote(strings.Repeat("x", maximumIndexedTextBytes+1)))
		_, err := decodeInput(collection, map[string]json.RawMessage{field.Name: overLimit})
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("decodeInput(%s over limit) error = %v, want validation", field.Name, err)
		}
		want := []ValidationDetail{{Field: field.Name, Code: "value_too_long"}}
		if !reflect.DeepEqual(validationErr.Details, want) {
			t.Fatalf("validation details = %#v, want %#v", validationErr.Details, want)
		}
	}

	unindexed := schema.Collection{Fields: []schema.Field{{Name: "body", Type: schema.TypeText}}}
	longBody := json.RawMessage(strconv.Quote(strings.Repeat("x", maximumIndexedTextBytes+1)))
	if _, err := decodeInput(unindexed, map[string]json.RawMessage{"body": longBody}); err != nil {
		t.Fatalf("unindexed text should remain unbounded: %v", err)
	}
}

func TestEncodeRecordReturnsCanonicalJSONContract(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	externalID := uuid.MustParse("12345678-1234-4567-8901-123456789abc")
	createdAt := time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.FixedZone("KST", 9*60*60))
	updatedAt := createdAt.Add(time.Minute)
	row := &fixtureRow{values: []any{
		pgtype.UUID{Bytes: id, Valid: true},
		createdAt,
		updatedAt,
		pgtype.Bool{Bool: true, Valid: true},
		pgtype.Int4{Int32: -42, Valid: true},
		pgtype.Int8{Int64: 9223372036854775807, Valid: true},
		pgtype.Text{String: "12345678901234567890.12345678901234567890", Valid: true},
		pgtype.Text{String: "hello", Valid: true},
		pgtype.Timestamptz{Time: createdAt, Valid: true},
		pgtype.UUID{Bytes: externalID, Valid: true},
		[]byte(`{"items":[1,2],"ok":true}`),
	}}

	got, err := EncodeRecord(allTypesCollection(), row)
	if err != nil {
		t.Fatalf("EncodeRecord() error = %v", err)
	}
	wantJSON := `{
		"id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		"created_at":"2026-08-28T03:34:56.123456789Z",
		"updated_at":"2026-08-28T03:35:56.123456789Z",
		"active":true,
		"count":-42,
		"large_count":"9223372036854775807",
		"amount":"12345678901234567890.12345678901234567890",
		"title":"hello",
		"published_at":"2026-08-28T03:34:56.123456789Z",
		"external_id":"12345678-1234-4567-8901-123456789abc",
		"metadata":{"items":[1,2],"ok":true}
	}`
	assertJSONEqual(t, got, wantJSON)
}

func TestEncodeRecordPreservesJSONBShapesAndSQLNulls(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{Fields: []schema.Field{
		{Name: "optional_text", ColumnName: "optional_text", Type: schema.TypeText, Ordinal: 0},
		{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB, Ordinal: 1},
	}}
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)

	for _, raw := range []string{`{"a":1}`, `[1,2]`, `"scalar"`, `123.45678901234567890`, `true`, `null`} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			row := &fixtureRow{values: []any{pgtype.UUID{Bytes: id, Valid: true}, now, now, pgtype.Text{}, []byte(raw)}}
			got, err := EncodeRecord(collection, row)
			if err != nil {
				t.Fatalf("EncodeRecord() error = %v", err)
			}
			if got["optional_text"] != nil {
				t.Fatalf("optional_text = %#v, want nil", got["optional_text"])
			}
			assertJSONEqual(t, got["metadata"], raw)
		})
	}
}

func TestEncodeRecordPreservesNumericZeroScale(t *testing.T) {
	t.Parallel()

	collection := schema.Collection{Fields: []schema.Field{
		{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric, Ordinal: 0},
	}}
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)

	for _, input := range []string{"0", "0.0", "0.00", "0.000000", "1.50", "-12.3400"} {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			row := &fixtureRow{values: []any{
				pgtype.UUID{Bytes: id, Valid: true}, now, now,
				pgtype.Text{String: input, Valid: true},
			}}
			got, err := EncodeRecord(collection, row)
			if err != nil {
				t.Fatalf("EncodeRecord() error = %v", err)
			}
			if got["amount"] != input {
				t.Fatalf("amount = %#v, want %q", got["amount"], input)
			}
		})
	}
}

func TestEncodeRecordRejectsInvalidDatabaseValues(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		collection schema.Collection
		value      any
	}{
		{name: "non-finite numeric", collection: schema.Collection{Fields: []schema.Field{{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric}}}, value: pgtype.Numeric{NaN: true, Valid: true}},
		{name: "invalid jsonb", collection: schema.Collection{Fields: []schema.Field{{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB}}}, value: []byte(`{}[]`)},
		{name: "unsupported type", collection: schema.Collection{Fields: []schema.Field{{Name: "broken", ColumnName: "broken", Type: schema.FieldType("money")}}}, value: pgtype.Text{String: "1", Valid: true}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := EncodeRecord(test.collection, &fixtureRow{values: []any{pgtype.UUID{Bytes: id, Valid: true}, now, now, test.value}})
			if err == nil {
				t.Fatal("EncodeRecord() error = nil, want error")
			}
		})
	}
}

func TestEncodeRecordReturnsOpaqueCategorizedScanFailure(t *testing.T) {
	t.Parallel()

	hostile := errors.New(`ERROR: duplicate key violates constraint (SQLSTATE 23505); INSERT INTO "app"."records" VALUES ('secret')`)
	_, err := EncodeRecord(schema.Collection{}, &fixtureRow{err: hostile})
	if err == nil {
		t.Fatal("EncodeRecord() error = nil, want scan error")
	}
	if err.Error() != "record scan failed" {
		t.Fatalf("EncodeRecord() error text = %q, want opaque constant", err.Error())
	}
	var scanErr *RecordScanError
	if !errors.As(err, &scanErr) {
		t.Fatalf("EncodeRecord() error type = %T, want stable *RecordScanError category", err)
	}
	if errors.Unwrap(err) != nil || errors.Is(err, hostile) {
		t.Fatalf("EncodeRecord() publicly exposes scan cause: %#v", err)
	}
	if !errors.Is(scanErr.diagnosticCause(), hostile) {
		t.Fatalf("internal diagnostic cause = %#v, want hostile scanner cause", scanErr.diagnosticCause())
	}
}

type fixtureRow struct {
	values []any
	err    error
}

func (r *fixtureRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != len(r.values) {
		return errors.New("fixture destination count mismatch")
	}
	for index := range destinations {
		destination := reflect.ValueOf(destinations[index])
		source := reflect.ValueOf(r.values[index])
		if !destination.IsValid() || destination.Kind() != reflect.Pointer || !source.IsValid() || !source.Type().AssignableTo(destination.Elem().Type()) {
			return errors.New("fixture destination type mismatch")
		}
		destination.Elem().Set(source)
	}
	return nil
}

func allTypesCollection() schema.Collection {
	return schema.Collection{
		Name:      "records",
		TableName: "records",
		Fields: []schema.Field{
			{Name: "active", ColumnName: "active", Type: schema.TypeBoolean, Ordinal: 0},
			{Name: "count", ColumnName: "count", Type: schema.TypeInteger, Ordinal: 1},
			{Name: "large_count", ColumnName: "large_count", Type: schema.TypeBigInt, Ordinal: 2},
			{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric, Ordinal: 3},
			{Name: "title", ColumnName: "title", Type: schema.TypeText, Ordinal: 4},
			{Name: "published_at", ColumnName: "published_at", Type: schema.TypeTimestampTZ, Ordinal: 5},
			{Name: "external_id", ColumnName: "external_id", Type: schema.TypeUUID, Ordinal: 6},
			{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB, Ordinal: 7},
		},
	}
}

func assertValue(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value = %#v, want %#v", got, want)
	}
}

func assertNumeric(t *testing.T, value any, want string) {
	t.Helper()
	numeric, ok := value.(pgtype.Numeric)
	if !ok {
		t.Fatalf("value type = %T, want pgtype.Numeric", value)
	}
	got, err := numeric.Value()
	if err != nil {
		t.Fatalf("numeric.Value() error = %v", err)
	}
	if got != want {
		t.Fatalf("numeric value = %#v, want %q", got, want)
	}
}

func assertJSONEqual(t *testing.T, got any, want string) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got JSON: %v", err)
	}
	var gotValue any
	gotDecoder := json.NewDecoder(bytes.NewReader(gotJSON))
	gotDecoder.UseNumber()
	if err := gotDecoder.Decode(&gotValue); err != nil {
		t.Fatalf("decode got JSON %s: %v", gotJSON, err)
	}
	var wantValue any
	wantDecoder := json.NewDecoder(bytes.NewReader([]byte(want)))
	wantDecoder.UseNumber()
	if err := wantDecoder.Decode(&wantValue); err != nil {
		t.Fatalf("decode want JSON %s: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", gotJSON, want)
	}
}
