package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/fieldvalue"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var decimalInput = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
var integerStringInput = regexp.MustCompile(`^-?[0-9]+$`)

// nonFiniteNumericLiterals holds every value PostgreSQL's numeric output can
// produce that this API cannot represent as a decimal string.
var nonFiniteNumericLiterals = map[string]bool{"NaN": true, "Infinity": true, "-Infinity": true}

// maximumUnindexedTextBytes restates the request body limit at the codec layer
// so a single text value stays bounded even where that limit is bypassed.
// Indexed and unique text keeps the much smaller schema.MaximumTextBytes,
// because PostgreSQL btree entries have a page-dependent limit below 2704
// bytes and deterministic headroom beats letting data-dependent compression
// decide whether a request becomes an opaque database failure. Neither bound
// is a whole-record invariant: patching several unindexed text fields in turn
// can still grow one record past the list page budget, and that invariant
// belongs to the write service which sees the merged record.
const maximumUnindexedTextBytes = 1 << 20

func decodeInput(collection schema.Collection, input map[string]json.RawMessage) (map[string]any, error) {
	fields := make(map[string]schema.Field, len(collection.Fields))
	for _, field := range collection.Fields {
		fields[field.Name] = field
	}

	names := make([]string, 0, len(input))
	for name := range input {
		names = append(names, name)
	}
	sort.Strings(names)
	decoded := make(map[string]any, len(input))
	for _, name := range names {
		raw := input[name]
		field, ok := fields[name]
		if !ok {
			return nil, validation(name, "unknown_field")
		}
		if field.Type == schema.TypeFile {
			return nil, validation(name, "file_field_read_only")
		}
		value, err := decodeFieldValue(field, raw)
		if err != nil {
			return nil, err
		}
		decoded[name] = value
	}
	return decoded, nil
}

func decodeFieldValue(field schema.Field, raw json.RawMessage) (any, error) {
	invalid := func() (any, error) { return nil, validation(field.Name, "type_mismatch") }
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		switch field.Type {
		case schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric,
			schema.TypeText, schema.TypeTimestampTZ, schema.TypeUUID, schema.TypeAutoDate, schema.TypePassword:
			if field.Required {
				return nil, validation(field.Name, "required")
			}
			return nil, nil
		case schema.TypeRelation:
			if field.Relation == nil || field.Relation.Cardinality != schema.RelationSingle {
				return nil, validation(field.Name, "relation_invalid_value")
			}
			if field.Relation.MinSelect > 0 {
				return nil, validation(field.Name, "relation_min_select")
			}
			return nil, nil
		}
	}
	switch field.Type {
	case schema.TypeBoolean:
		if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
			return invalid()
		}
		return bytes.Equal(raw, []byte("true")), nil
	case schema.TypeInteger:
		value, err := strconv.ParseInt(string(raw), 10, 32)
		if err != nil {
			return invalid()
		}
		if code := fieldvalue.ValidateNumeric(strconv.FormatInt(value, 10), codecOptions(field)); code != "" {
			return nil, validation(field.Name, code)
		}
		return int32(value), nil
	case schema.TypeBigInt:
		value, ok := jsonString(raw)
		if !ok || !integerStringInput.MatchString(value) {
			return invalid()
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return invalid()
		}
		if code := fieldvalue.ValidateNumeric(value, codecOptions(field)); code != "" {
			return nil, validation(field.Name, code)
		}
		return parsed, nil
	case schema.TypeNumeric:
		value, ok := jsonString(raw)
		if !ok || !decimalInput.MatchString(value) || !jsonvalue.ValidPostgresNumber(value) {
			return invalid()
		}
		var numeric pgtype.Numeric
		if err := numeric.Scan(value); err != nil || !numeric.Valid || numeric.NaN || numeric.InfinityModifier != pgtype.Finite {
			return invalid()
		}
		if code := fieldvalue.ValidateNumeric(value, codecOptions(field)); code != "" {
			return nil, validation(field.Name, code)
		}
		return numeric, nil
	case schema.TypeText, schema.TypeEditor:
		value, ok := jsonString(raw)
		if !ok {
			return invalid()
		}
		if (field.Indexed || field.Unique) && len(value) > schema.MaximumTextBytes {
			return nil, validation(field.Name, "value_too_long")
		}
		maximum := int64(maximumUnindexedTextBytes)
		if field.Type == schema.TypeEditor {
			maximum = editorMaximumSize(field)
		}
		if int64(len(value)) > maximum {
			return nil, validation(field.Name, "value_too_long")
		}
		if field.Type == schema.TypeEditor && field.Required && value == "" {
			return nil, validation(field.Name, "required")
		}
		if field.Type != schema.TypeEditor {
			if code := fieldvalue.ValidateText(value, codecOptions(field)); code != "" {
				return nil, validation(field.Name, code)
			}
		}
		return value, nil
	case schema.TypePassword:
		value, ok := jsonString(raw)
		if !ok {
			return invalid()
		}
		return value, nil
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		value, ok := jsonString(raw)
		if !ok {
			return invalid()
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || !hasRFC3339Offset(value) {
			return invalid()
		}
		return parsed.UTC(), nil
	case schema.TypeUUID:
		value, ok := jsonString(raw)
		if !ok {
			return invalid()
		}
		parsed, err := uuid.Parse(value)
		if err != nil {
			return invalid()
		}
		return parsed, nil
	case schema.TypeRelation:
		return decodeRelationFieldValue(field, raw)
	case schema.TypeGeoPoint:
		return decodeGeoPoint(field, raw)
	case schema.TypeJSONB:
		if !validJSON(raw) {
			return invalid()
		}
		if len(field.Options.AllowedValues) > 0 {
			var values []string
			if json.Unmarshal(raw, &values) != nil || values == nil {
				return nil, validation(field.Name, "select_invalid_value")
			}
			if code := fieldvalue.ValidateSelection(values, codecOptions(field)); code != "" {
				return nil, validation(field.Name, code)
			}
		}
		return append(json.RawMessage(nil), raw...), nil
	default:
		return nil, validation(field.Name, "unsupported_type")
	}
}

func codecOptions(field schema.Field) fieldvalue.Options {
	o := field.Options
	return fieldvalue.Options{MinLength: o.MinLength, MaxLength: o.MaxLength, MinSelect: o.MinSelect, MaxSelect: o.MaxSelect, Pattern: o.Pattern, Format: o.Format, AllowedDomains: o.AllowedDomains, DeniedDomains: o.DeniedDomains, AllowedValues: o.AllowedValues, Min: o.Min, Max: o.Max}
}

func decodeRelationFieldValue(field schema.Field, raw json.RawMessage) (any, error) {
	if field.Relation == nil {
		return nil, validation(field.Name, "relation_invalid_value")
	}
	spec := field.Relation
	if spec.Cardinality == schema.RelationSingle {
		value, ok := jsonString(raw)
		if !ok {
			return nil, validation(field.Name, "relation_invalid_value")
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, validation(field.Name, "relation_invalid_value")
		}
		return parsed, nil
	}
	if spec.Cardinality != schema.RelationMultiple {
		return nil, validation(field.Name, "relation_invalid_value")
	}

	var encoded []json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded == nil {
		return nil, validation(field.Name, "relation_invalid_value")
	}
	if len(encoded) < spec.MinSelect {
		return nil, validation(field.Name, "relation_min_select")
	}
	if len(encoded) > spec.MaxSelect {
		return nil, validation(field.Name, "relation_max_select")
	}
	values := make([]uuid.UUID, 0, len(encoded))
	seen := make(map[uuid.UUID]struct{}, len(encoded))
	for _, item := range encoded {
		value, ok := jsonString(item)
		if !ok {
			return nil, validation(field.Name, "relation_invalid_value")
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, validation(field.Name, "relation_invalid_value")
		}
		if _, duplicate := seen[parsed]; duplicate {
			return nil, validation(field.Name, "duplicate_relation_value")
		}
		seen[parsed] = struct{}{}
		values = append(values, parsed)
	}
	return values, nil
}

func EncodeRecord(collection schema.Collection, row RowScanner) (map[string]any, error) {
	fields := fieldsByOrdinal(collection)
	var id pgtype.UUID
	var createdAt, updatedAt time.Time
	destinations := []any{&id, &createdAt, &updatedAt}
	values := make([]any, len(fields))
	for index, field := range fields {
		if field.Type == schema.TypeFile {
			continue
		}
		if isMultipleRelation(field) {
			continue
		}
		destination, err := scanDestination(field)
		if err != nil {
			return nil, err
		}
		values[index] = destination
		destinations = append(destinations, destination)
	}
	if err := row.Scan(destinations...); err != nil {
		return nil, recordScanError(err)
	}

	record := make(map[string]any, len(fields)+3)
	if !id.Valid {
		return nil, fmt.Errorf("encode record id: invalid database value")
	}
	record["id"] = uuid.UUID(id.Bytes).String()
	record["created_at"] = createdAt.UTC().Format(time.RFC3339Nano)
	record["updated_at"] = updatedAt.UTC().Format(time.RFC3339Nano)
	for index, field := range fields {
		if field.Type == schema.TypeFile {
			continue
		}
		if isMultipleRelation(field) {
			record[field.Name] = []string{}
			continue
		}
		encoded, err := encodeScannedValue(field, values[index])
		if err != nil {
			return nil, err
		}
		record[field.Name] = encoded
	}
	return record, nil
}

func scanDestination(field schema.Field) (any, error) {
	switch field.Type {
	case schema.TypeBoolean:
		return &pgtype.Bool{}, nil
	case schema.TypeInteger:
		return &pgtype.Int4{}, nil
	case schema.TypeBigInt:
		return &pgtype.Int8{}, nil
	case schema.TypeNumeric:
		return &pgtype.Text{}, nil
	case schema.TypeText, schema.TypeEditor, schema.TypePassword:
		return &pgtype.Text{}, nil
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		return &pgtype.Timestamptz{}, nil
	case schema.TypeUUID:
		return &pgtype.UUID{}, nil
	case schema.TypeRelation:
		if field.Relation == nil || field.Relation.Cardinality != schema.RelationSingle {
			return nil, validation(field.Name, "relation_invalid_value")
		}
		return &pgtype.UUID{}, nil
	case schema.TypeJSONB, schema.TypeGeoPoint:
		value := []byte(nil)
		return &value, nil
	default:
		return nil, validation(field.Name, "unsupported_type")
	}
}

func editorMaximumSize(field schema.Field) int64 {
	if field.Options.MaxSize != nil && *field.Options.MaxSize > 0 {
		return *field.Options.MaxSize
	}
	return 5 << 20
}

func decodeGeoPoint(field schema.Field, raw json.RawMessage) (any, error) {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != 2 {
		return nil, validation(field.Name, "type_mismatch")
	}
	coordinates := map[string]float64{}
	for _, name := range []string{"lon", "lat"} {
		member, ok := members[name]
		var coordinate float64
		if !ok || json.Unmarshal(member, &coordinate) != nil || math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
			return nil, validation(field.Name, "type_mismatch")
		}
		coordinates[name] = coordinate
	}
	if coordinates["lat"] < -90 || coordinates["lat"] > 90 {
		return nil, validation(field.Name, "invalid_latitude")
	}
	if coordinates["lon"] < -180 || coordinates["lon"] > 180 {
		return nil, validation(field.Name, "invalid_longitude")
	}
	if field.Required && coordinates["lat"] == 0 && coordinates["lon"] == 0 {
		return nil, validation(field.Name, "required")
	}
	canonical, _ := json.Marshal(map[string]float64{"lon": coordinates["lon"], "lat": coordinates["lat"]})
	return json.RawMessage(canonical), nil
}

func isMultipleRelation(field schema.Field) bool {
	return field.Type == schema.TypeRelation && field.Relation != nil && field.Relation.Cardinality == schema.RelationMultiple
}

func encodeScannedValue(field schema.Field, destination any) (any, error) {
	invalid := func() (any, error) { return nil, fmt.Errorf("encode field %s: invalid database value", field.Name) }
	switch value := destination.(type) {
	case *pgtype.Bool:
		if !value.Valid {
			return nil, nil
		}
		return value.Bool, nil
	case *pgtype.Int4:
		if !value.Valid {
			return nil, nil
		}
		return value.Int32, nil
	case *pgtype.Int8:
		if !value.Valid {
			return nil, nil
		}
		return strconv.FormatInt(value.Int64, 10), nil
	case *pgtype.Text:
		if !value.Valid {
			return nil, nil
		}
		if field.Type == schema.TypePassword {
			return "", nil
		}
		// scanDestination reads numeric columns as text, so this is the only
		// place a non-finite numeric written outside the API (operator SQL)
		// can be caught. Text fields keep accepting the same literals as data.
		if field.Type == schema.TypeNumeric && nonFiniteNumericLiterals[value.String] {
			return invalid()
		}
		return value.String, nil
	case *pgtype.Timestamptz:
		if !value.Valid || value.InfinityModifier != pgtype.Finite {
			if !value.Valid {
				return nil, nil
			}
			return invalid()
		}
		return value.Time.UTC().Format(time.RFC3339Nano), nil
	case *pgtype.UUID:
		if !value.Valid {
			return nil, nil
		}
		return uuid.UUID(value.Bytes).String(), nil
	case *[]byte:
		if *value == nil {
			return nil, nil
		}
		if !validJSON(*value) {
			return invalid()
		}
		return append(json.RawMessage(nil), (*value)...), nil
	default:
		return invalid()
	}
}

func fieldsByOrdinal(collection schema.Collection) []schema.Field {
	fields := append([]schema.Field(nil), collection.Fields...)
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Ordinal == fields[right].Ordinal {
			return fields[left].Name < fields[right].Name
		}
		return fields[left].Ordinal < fields[right].Ordinal
	})
	return fields
}

func jsonString(raw json.RawMessage) (string, bool) {
	value, err := jsonvalue.String(raw)
	if err != nil {
		return "", false
	}
	return value, true
}

func hasRFC3339Offset(value string) bool {
	if len(value) < len("2006-01-02T15:04:05Z") {
		return false
	}
	if value[len(value)-1] == 'Z' {
		return true
	}
	return len(value) >= 6 && (value[len(value)-6] == '+' || value[len(value)-6] == '-') && value[len(value)-3] == ':'
}

func validJSON(raw []byte) bool {
	return jsonvalue.Valid(raw)
}
