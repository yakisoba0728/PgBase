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

func normalizeDefault(typ FieldType, raw json.RawMessage) (json.RawMessage, string, error) {
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
	case TypeInteger:
		value, ok := parseInteger(raw, 32)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return jsonString(value), value, nil
	case TypeBigInt:
		value, ok := parseInteger(raw, 64)
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
	case TypeText:
		value, ok := parseJSONString(raw)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		literal, ok := sqlLiteral(value)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		return jsonString(value), literal + "::text", nil
	case TypeTimestampTZ:
		return normalizeTimestampDefault(raw)
	case TypeUUID:
		value, ok := parseUUID(raw)
		if !ok {
			return nil, "", validation("", "invalid_default")
		}
		literal, _ := sqlLiteral(value)
		return jsonString(value), literal + "::uuid", nil
	case TypeJSONB:
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
	if !ok || !decimalLiteral.MatchString(value) || !jsonvalue.ValidPostgresNumber(value) {
		return "", false
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
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
	if err != nil {
		return "", false
	}
	return value, true
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
	if err != nil {
		return nil, false
	}
	return json.RawMessage(canonical), true
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
