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
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
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

func TestPocketBaseModeRejectsRequiredZeroAndBlankValues(t *testing.T) {
	collection := schema.Collection{ValidationMode: schema.ValidationModePocketBase, Fields: []schema.Field{
		{Name: "enabled", Type: schema.TypeBoolean, Required: true},
		{Name: "count", Type: schema.TypeInteger, Required: true},
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "metadata", Type: schema.TypeJSONB, Required: true},
	}}
	for name, raw := range map[string]string{"enabled": "false", "count": "0", "title": `""`, "metadata": "{}"} {
		if _, err := decodeInput(collection, map[string]json.RawMessage{name: json.RawMessage(raw)}); err == nil {
			t.Fatalf("%s accepted PB blank %s", name, raw)
		}
	}
	collection.ValidationMode = schema.ValidationModePostgres
	if _, err := decodeInput(collection, map[string]json.RawMessage{"enabled": json.RawMessage("false"), "count": json.RawMessage("0"), "title": json.RawMessage(`""`), "metadata": json.RawMessage("{}")}); err != nil {
		t.Fatalf("postgres zero values: %v", err)
	}
}

func TestTimestampBoundsAndJSONMaximumSize(t *testing.T) {
	minimum, maximum := "2026-01-01T00:00:00Z", "2026-12-31T23:59:59Z"
	date := schema.Field{Name: "when", Type: schema.TypeTimestampTZ, Options: schema.FieldOptions{Min: &minimum, Max: &maximum}}
	if _, err := decodeFieldValue(date, json.RawMessage(`"2025-12-31T23:59:59Z"`)); err == nil {
		t.Fatal("accepted date below minimum")
	}
	if _, err := decodeFieldValue(date, json.RawMessage(`"2026-06-01T00:00:00+09:00"`)); err != nil {
		t.Fatalf("date in range: %v", err)
	}
	limit := int64(2)
	field := schema.Field{Name: "data", Type: schema.TypeJSONB, Options: schema.FieldOptions{MaxSize: &limit}}
	if _, err := decodeFieldValue(field, json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("accepted oversized JSON")
	}
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

func TestDecodeInputBoundsTextByIndexAndByRequestBodySize(t *testing.T) {
	t.Parallel()

	assertTooLong := func(t *testing.T, field schema.Field, size int) {
		t.Helper()
		collection := schema.Collection{Fields: []schema.Field{field}}
		raw := json.RawMessage(strconv.Quote(strings.Repeat("x", size)))
		_, err := decodeInput(collection, map[string]json.RawMessage{field.Name: raw})
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("decodeInput(%s, %d bytes) error = %v, want validation", field.Name, size, err)
		}
		want := []ValidationDetail{{Field: field.Name, Code: "value_too_long"}}
		if !reflect.DeepEqual(validationErr.Details, want) {
			t.Fatalf("validation details = %#v, want %#v", validationErr.Details, want)
		}
	}
	assertAccepted := func(t *testing.T, field schema.Field, size int) {
		t.Helper()
		collection := schema.Collection{Fields: []schema.Field{field}}
		raw := json.RawMessage(strconv.Quote(strings.Repeat("x", size)))
		if _, err := decodeInput(collection, map[string]json.RawMessage{field.Name: raw}); err != nil {
			t.Fatalf("decodeInput(%s, %d bytes) error = %v", field.Name, size, err)
		}
	}

	for _, field := range []schema.Field{
		{Name: "indexed", Type: schema.TypeText, Indexed: true},
		{Name: "unique", Type: schema.TypeText, Unique: true},
	} {
		assertAccepted(t, field, schema.MaximumTextBytes)
		assertTooLong(t, field, schema.MaximumTextBytes+1)
	}

	// Unindexed text is not held to the btree bound - long documents are a
	// supported product capability - but it is still bounded, so one field
	// cannot grow without limit when the request body limit is bypassed.
	body := schema.Field{Name: "body", Type: schema.TypeText}
	assertAccepted(t, body, schema.MaximumTextBytes+1)
	assertAccepted(t, body, maximumUnindexedTextBytes)
	assertTooLong(t, body, maximumUnindexedTextBytes+1)
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
		{name: "numeric NaN", collection: numericCollection(), value: pgtype.Text{String: "NaN", Valid: true}},
		{name: "numeric Infinity", collection: numericCollection(), value: pgtype.Text{String: "Infinity", Valid: true}},
		{name: "numeric negative Infinity", collection: numericCollection(), value: pgtype.Text{String: "-Infinity", Valid: true}},
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

// TestEncodeRecordKeepsNonFiniteLiteralsInTextFields proves the rejection above
// is scoped to numeric columns rather than to the literal text, so the guard
// cannot pass for the wrong reason.
func TestEncodeRecordKeepsNonFiniteLiteralsInTextFields(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	collection := schema.Collection{Fields: []schema.Field{{Name: "label", ColumnName: "label", Type: schema.TypeText}}}
	record, err := EncodeRecord(collection, &fixtureRow{values: []any{
		pgtype.UUID{Bytes: id, Valid: true}, now, now, pgtype.Text{String: "NaN", Valid: true},
	}})
	if err != nil {
		t.Fatalf("EncodeRecord() error = %v", err)
	}
	if record["label"] != "NaN" {
		t.Fatalf("label = %#v, want the literal text preserved", record["label"])
	}
}

func numericCollection() schema.Collection {
	return schema.Collection{Fields: []schema.Field{{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric}}}
}

// The two tests below cover page assembly and relation planning error mapping.

func TestFinalizeRecordPageTrimsToTheAdvertisedBoundAndAnchorsTheCursor(t *testing.T) {
	t.Parallel()

	service := &Service{cursors: cursor.NewCodec(bytes.Repeat([]byte("k"), 32))}
	collectionID := uuid.MustParse("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff")
	sortKeys := effectiveSortKeys(nil)
	expiresAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	// Ten individually valid records of roughly 6 MiB overflow the 48 MiB wire
	// bound, so the page must
	// lose its tail and still carry a cursor anchored at the last kept item.
	page := RecordPage{}
	for index := 0; index < 10; index++ {
		page.Items = append(page.Items, map[string]any{
			"id":         uuid.NewSHA1(collectionID, []byte{byte(index)}).String(),
			"created_at": time.Date(2026, 8, 28, 0, 0, index, 0, time.UTC).Format(time.RFC3339Nano),
			"payload":    strings.Repeat("x", 6<<20),
		})
	}
	page, err := assembleRecordPageForTest(service, page.Items, collectionID, sortKeys, expiresAt, true)
	if err != nil {
		t.Fatalf("pageAssembler.finish() error = %v", err)
	}
	if len(page.Items) == 0 || len(page.Items) >= 10 {
		t.Fatalf("kept %d items, want the tail trimmed", len(page.Items))
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)+1 > maximumRecordPageBytes {
		t.Fatalf("finalized page = %d bytes, want at most %d", len(encoded)+1, maximumRecordPageBytes)
	}
	if page.NextCursor == "" {
		t.Fatal("trimmed page lost its continuation cursor")
	}
	decoded, err := service.cursors.Decode(page.NextCursor, collectionID, expiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("decode continuation cursor: %v", err)
	}
	anchor := page.Items[len(page.Items)-1]
	wantValues := []string{strconv.Quote(anchor["created_at"].(string)), strconv.Quote(anchor["id"].(string))}
	if len(decoded.Values) != len(wantValues) {
		t.Fatalf("cursor values = %v, want %v", decoded.Values, wantValues)
	}
	for index, want := range wantValues {
		if string(decoded.Values[index]) != want {
			t.Fatalf("cursor value %d = %s, want %s (cursor must anchor at the last kept item)", index, decoded.Values[index], want)
		}
	}

	single := RecordPage{Items: []map[string]any{{
		"id":         uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee").String(),
		"created_at": time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		"payload":    strings.Repeat("x", maximumRecordPageBytes),
	}}}
	_, err = assembleRecordPageForTest(service, single.Items, collectionID, sortKeys, expiresAt, true)
	assertValidationDetail(t, err, "response", "response_too_large")
}

func assembleRecordPageForTest(
	service *Service,
	items []map[string]any,
	collectionID uuid.UUID,
	sortKeys []SortKey,
	expiresAt time.Time,
	hasMore bool,
) (RecordPage, error) {
	candidates := make([]pageCandidate, len(items))
	for index, item := range items {
		values, err := recordSortValues(sortKeys, item)
		if err != nil {
			return RecordPage{}, err
		}
		candidates[index] = pageCandidate{id: uuid.MustParse(item[systemFieldID].(string)), root: item, wire: item, cursorValues: values}
	}
	assembler := pageAssembler{
		cursors: service.cursors, order: pageOrderForTest(collectionID, sortKeys),
		expiresAt: expiresAt, maximumBytes: maximumRecordPageBytes,
		cursorV5: pageDescriptorForTest(sortKeys),
	}
	keep, cursor, err := assembler.finish(candidates, hasMore)
	if err != nil {
		return RecordPage{}, err
	}
	return RecordPage{Items: items[:keep], NextCursor: cursor}, nil
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
