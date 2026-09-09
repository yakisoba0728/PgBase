package schema

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
)

var decimalLiteral = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

// MaximumTextBytes bounds indexed and unique text values, because PostgreSQL
// btree entries have a page-dependent limit below 2704 bytes. Every text
// default a request supplies carries the same bound regardless of the field's
// index settings, so the catalog never accepts a default the record write path
// rejects on write-back and making a field indexed later cannot strand its own
// default. The bound is an input rule only; see normalizeStoredDefault.
const MaximumTextBytes = 2048

// normalizeDefault validates and renders a default a schema request supplied.
// It carries every bound this binary refuses to accept, including the text
// byte bound, so plan and apply reject a default the record write path could
// not later write back.
func normalizeDefault(typ FieldType, raw json.RawMessage) (json.RawMessage, string, error) {
	return normalizeTypedDefault(typ, raw, true)
}

// normalizeStoredDefault validates a default this or an older binary already
// wrote to the catalog. Bounds introduced after a release must not appear
// here: validateCatalogIntegrity re-normalizes every stored default on every
// EnsureCurrent, so a bound a stored default fails turns one legacy column
// into ErrConflict for every schema and record operation in the database
// rather than for the one collection carrying it. This mirrors
// reservedInputNames, which closes a name to new schemas without locking
// catalogs that already hold it.
//
// validateCatalogIntegrity calls this function, so a database holding a legacy
// oversized text default stays readable across the upgrade. internal/doctor
// reports the condition so an operator can find and shrink it.
func normalizeStoredDefault(typ FieldType, raw json.RawMessage) (json.RawMessage, string, error) {
	return normalizeTypedDefault(typ, raw, false)
}

// normalizeTypedDefault renders one default. boundText applies MaximumTextBytes,
// which only the input path may enforce.
func normalizeTypedDefault(typ FieldType, raw json.RawMessage, boundText bool) (json.RawMessage, string, error) {
	switch typ {
	case TypeBoolean:
		var value bool
		if !isJSONBoolean(raw) || json.Unmarshal(raw, &value) != nil {
			return nil, "", validation("", "invalid_default")
		}
		if value {
			return json.RawMessage("true"), "TRUE", nil
		}
		return json.RawMessage("false"), "FALSE", nil
	case TypeInteger, TypeBigInt:
		bits := 64
		if typ == TypeInteger {
			bits = 32
		}
		value, ok := parseInteger(raw, bits)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return jsonString(value), value, nil
	case TypeNumeric:
		value, ok := parseNumeric(raw)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return jsonString(value), value + "::numeric", nil
	case TypeText, TypeEditor, TypePassword:
		value, ok := parseJSONString(raw)
		if !ok || (boundText && len(value) > MaximumTextBytes) {
			return nil, "", validation("", "invalid_default")
		}
		literal, ok := sqlLiteral(value)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return jsonString(value), literal + "::text", nil
	case TypeTimestampTZ, TypeAutoDate:
		return normalizeTimestampDefault(raw)
	case TypeUUID:
		value, ok := parseUUID(raw)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		literal, _ := sqlLiteral(value)
		return jsonString(value), literal + "::uuid", nil
	case TypeJSONB, TypeGeoPoint:
		value, ok := canonicalJSON(raw)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		literal, ok := sqlLiteral(string(value))
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return value, literal + "::jsonb", nil
	default:
		return nil, "", validation("", "unsupported_type")
	}
}

func isJSONBoolean(raw json.RawMessage) bool {
	return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
}

func parseInteger(raw json.RawMessage, bits int) (string, bool) {
	value, ok := parseJSONString(raw)
	if !ok || !regexp.MustCompile(`^-?[0-9]+$`).MatchString(value) {
		return "", false
	}
	parsed, err := strconv.ParseInt(value, 10, bits)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(parsed, 10), true
}

func parseNumeric(raw json.RawMessage) (string, bool) {
	value, ok := parseJSONString(raw)
	if !ok || !decimalLiteral.MatchString(value) {
		return "", false
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	postgresLexeme := whole
	if len(parts) == 2 {
		postgresLexeme += "." + parts[1]
	}
	if negative {
		postgresLexeme = "-" + postgresLexeme
	}
	if !jsonvalue.ValidPostgresNumber(postgresLexeme) {
		return "", false
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	value = whole
	if fraction != "" {
		value += "." + fraction
	}
	if negative && value != "0" {
		value = "-" + value
	}
	return value, true
}

func parseJSONString(raw json.RawMessage) (string, bool) {
	value, err := jsonvalue.String(raw)
	return value, err == nil
}

func parseUUID(raw json.RawMessage) (string, bool) {
	value, ok := parseJSONString(raw)
	if !ok || len(value) != 36 {
		return "", false
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func normalizeTimestampDefault(raw json.RawMessage) (json.RawMessage, string, error) {
	if canonical, ok := canonicalJSON(raw); ok && bytes.Equal(canonical, []byte(`{"kind":"now"}`)) {
		return json.RawMessage(`{"kind":"now"}`), "CURRENT_TIMESTAMP", nil
	}
	value, ok := parseJSONString(raw)
	if !ok {
		return nil, "", validation("", "invalid_default")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Year() < 1 {
		return nil, "", validation("", "invalid_default")
	}
	// PostgreSQL stores timestamps at microsecond precision, so a value within
	// half a microsecond of the year 10000 boundary carries into a five digit
	// year that RFC3339 cannot parse back. Reject it here instead of emitting a
	// default the catalog and the record write path can no longer read.
	if parsed.Round(time.Microsecond).Year() > 9999 {
		return nil, "", validation("", "invalid_default")
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return nil, "", validation("", "invalid_default")
	}
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	literal, _ := sqlLiteral(canonical)
	return jsonString(canonical), literal + "::timestamptz", nil
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, bool) {
	canonical, err := jsonvalue.Canonical(raw)
	return json.RawMessage(canonical), err == nil
}

func jsonString(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return json.RawMessage(encoded)
}

func sqlLiteral(value string) (string, bool) {
	if strings.IndexByte(value, 0) >= 0 {
		return "", false
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", "''")
	return "E'" + escaped + "'", true
}
