package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var decimalInput = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
var integerStringInput = regexp.MustCompile(`^-?[0-9]+$`)

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
		return parsed, nil
	case schema.TypeNumeric:
		value, ok := jsonString(raw)
		if !ok || !decimalInput.MatchString(value) {
			return invalid()
		}
		var numeric pgtype.Numeric
		if err := numeric.Scan(value); err != nil || !numeric.Valid || numeric.NaN || numeric.InfinityModifier != pgtype.Finite {
			return invalid()
		}
		return numeric, nil
	case schema.TypeText:
		value, ok := jsonString(raw)
		if !ok {
			return invalid()
		}
		return value, nil
	case schema.TypeTimestampTZ:
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
	case schema.TypeJSONB:
		if !validJSON(raw) {
			return invalid()
		}
		return append(json.RawMessage(nil), raw...), nil
	default:
		return nil, validation(field.Name, "unsupported_type")
	}
}

func EncodeRecord(collection schema.Collection, row RowScanner) (map[string]any, error) {
	fields := fieldsByOrdinal(collection)
	var id pgtype.UUID
	var createdAt, updatedAt time.Time
	destinations := []any{&id, &createdAt, &updatedAt}
	values := make([]any, len(fields))
	for index, field := range fields {
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
		return &pgtype.Numeric{}, nil
	case schema.TypeText:
		return &pgtype.Text{}, nil
	case schema.TypeTimestampTZ:
		return &pgtype.Timestamptz{}, nil
	case schema.TypeUUID:
		return &pgtype.UUID{}, nil
	case schema.TypeJSONB:
		value := []byte(nil)
		return &value, nil
	default:
		return nil, validation(field.Name, "unsupported_type")
	}
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
	case *pgtype.Numeric:
		if !value.Valid {
			return nil, nil
		}
		if value.NaN || value.InfinityModifier != pgtype.Finite {
			return invalid()
		}
		driverValue, err := value.Value()
		if err != nil {
			return nil, fmt.Errorf("encode field %s: %w", field.Name, err)
		}
		text, ok := driverValue.(string)
		if !ok {
			return invalid()
		}
		return text, nil
	case *pgtype.Text:
		if !value.Valid {
			return nil, nil
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
	if len(raw) < 2 || raw[0] != '"' {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}
