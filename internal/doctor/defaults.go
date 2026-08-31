package doctor

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var (
	physicalIntegerDefault = regexp.MustCompile(`^-?[0-9]+$`)
	physicalDecimalDefault = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

// physicalDefaultMatchesCatalog compares the catalog's typed JSON default with
// PostgreSQL's deparsed column default without executing the physical
// expression. Unknown expressions fail closed as drift instead of allowing a
// function-bearing default to run during a read-only diagnostic.
func physicalDefaultMatchesCatalog(fieldType, catalogJSON, physical string) bool {
	raw := json.RawMessage(catalogJSON)
	switch schema.FieldType(fieldType) {
	case schema.TypeBoolean:
		var expected bool
		if json.Unmarshal(raw, &expected) != nil {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(physical), strconv.FormatBool(expected))
	case schema.TypeInteger:
		return integerDefaultMatches(raw, physical, 32)
	case schema.TypeBigInt:
		return integerDefaultMatches(raw, physical, 64)
	case schema.TypeNumeric:
		expected, err := jsonvalue.String(raw)
		if err != nil {
			return false
		}
		actual, ok := numericExpressionValue(physical)
		if !ok {
			return false
		}
		expected, ok = canonicalDecimal(expected)
		if !ok {
			return false
		}
		actual, ok = canonicalDecimal(actual)
		return ok && actual == expected
	case schema.TypeText:
		expected, err := jsonvalue.String(raw)
		if err != nil {
			return false
		}
		actual, ok := postgresLiteralCastValue(physical, "text")
		return ok && actual == expected
	case schema.TypeTimestampTZ:
		canonical, err := jsonvalue.Canonical(raw)
		if err == nil && bytes.Equal(canonical, []byte(`{"kind":"now"}`)) {
			return strings.EqualFold(strings.TrimSpace(physical), "CURRENT_TIMESTAMP")
		}
		expectedText, err := jsonvalue.String(raw)
		if err != nil {
			return false
		}
		expected, err := time.Parse(time.RFC3339Nano, expectedText)
		if err != nil {
			return false
		}
		actualText, ok := postgresLiteralCastValue(physical, "timestamp with time zone")
		if !ok {
			return false
		}
		actual, ok := parsePostgresTimestamp(actualText)
		return ok && actual.Equal(postgresTimestampPrecision(expected))
	case schema.TypeUUID:
		expectedText, err := jsonvalue.String(raw)
		if err != nil {
			return false
		}
		expected, err := uuid.Parse(expectedText)
		if err != nil {
			return false
		}
		actualText, ok := postgresLiteralCastValue(physical, "uuid")
		if !ok {
			return false
		}
		actual, err := uuid.Parse(actualText)
		return err == nil && actual == expected
	case schema.TypeJSONB:
		expected, err := jsonvalue.Canonical(raw)
		if err != nil {
			return false
		}
		actualText, ok := postgresLiteralCastValue(physical, "jsonb")
		if !ok {
			return false
		}
		actual, err := jsonvalue.Canonical([]byte(actualText))
		return err == nil && bytes.Equal(actual, expected)
	default:
		return false
	}
}

// PostgreSQL stores timestamp values at microsecond precision and resolves an
// exact half-microsecond using round-to-even. time.Time.Round resolves ties
// upward instead, which would report healthy catalog defaults as drift.
func postgresTimestampPrecision(value time.Time) time.Time {
	nanoseconds := value.Nanosecond()
	microseconds := nanoseconds / int(time.Microsecond)
	remainder := nanoseconds % int(time.Microsecond)
	if remainder > int(time.Microsecond)/2 || remainder == int(time.Microsecond)/2 && microseconds%2 != 0 {
		microseconds++
	}
	wholeSecond := value.Add(-time.Duration(nanoseconds))
	return wholeSecond.Add(time.Duration(microseconds) * time.Microsecond)
}

func integerDefaultMatches(raw json.RawMessage, physical string, bits int) bool {
	expectedText, err := jsonvalue.String(raw)
	if err != nil {
		return false
	}
	expected, err := strconv.ParseInt(expectedText, 10, bits)
	if err != nil {
		return false
	}
	actualText, ok := numericExpressionValue(physical)
	if !ok || !physicalIntegerDefault.MatchString(actualText) {
		return false
	}
	actual, err := strconv.ParseInt(actualText, 10, bits)
	return err == nil && actual == expected
}

func numericExpressionValue(expression string) (string, bool) {
	value := strings.ReplaceAll(strings.TrimSpace(expression), " ", "")
	for {
		previous := value
		value = trimOuterParentheses(value)
		lower := strings.ToLower(value)
		for _, suffix := range []string{"::numeric", "::bigint", "::integer"} {
			if strings.HasSuffix(lower, suffix) {
				value = strings.TrimSpace(value[:len(value)-len(suffix)])
				break
			}
		}
		if value == previous {
			break
		}
	}
	value = trimOuterParentheses(value)
	if strings.HasPrefix(value, "'") || strings.HasPrefix(value, "E'") || strings.HasPrefix(value, "e'") {
		var ok bool
		value, ok = parsePostgresStringLiteral(value)
		if !ok {
			return "", false
		}
	}
	if !physicalDecimalDefault.MatchString(value) {
		return "", false
	}
	return value, true
}

func canonicalDecimal(value string) (string, bool) {
	if !physicalDecimalDefault.MatchString(value) {
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

func postgresLiteralCastValue(expression, cast string) (string, bool) {
	value := strings.TrimSpace(expression)
	suffix := "::" + cast
	if len(value) <= len(suffix) || !strings.EqualFold(value[len(value)-len(suffix):], suffix) {
		return "", false
	}
	return parsePostgresStringLiteral(strings.TrimSpace(value[:len(value)-len(suffix)]))
}

func parsePostgresStringLiteral(literal string) (string, bool) {
	escaped := false
	if strings.HasPrefix(literal, "E'") || strings.HasPrefix(literal, "e'") {
		escaped = true
		literal = literal[1:]
	}
	if len(literal) < 2 || literal[0] != '\'' || literal[len(literal)-1] != '\'' {
		return "", false
	}
	inner := literal[1 : len(literal)-1]
	var result strings.Builder
	for index := 0; index < len(inner); index++ {
		character := inner[index]
		if character == '\'' {
			if index+1 >= len(inner) || inner[index+1] != '\'' {
				return "", false
			}
			result.WriteByte('\'')
			index++
			continue
		}
		if escaped && character == '\\' {
			if index+1 >= len(inner) {
				return "", false
			}
			index++
			switch inner[index] {
			case '\\', '\'':
				result.WriteByte(inner[index])
			case 'n':
				result.WriteByte('\n')
			case 'r':
				result.WriteByte('\r')
			case 't':
				result.WriteByte('\t')
			case 'b':
				result.WriteByte('\b')
			case 'f':
				result.WriteByte('\f')
			default:
				return "", false
			}
			continue
		}
		result.WriteByte(character)
	}
	return result.String(), true
}

func trimOuterParentheses(value string) string {
	for len(value) >= 2 && value[0] == '(' && value[len(value)-1] == ')' {
		depth := 0
		wrapsAll := true
		for index, character := range value {
			switch character {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && index != len(value)-1 {
					wrapsAll = false
				}
			}
			if depth < 0 {
				return value
			}
		}
		if !wrapsAll || depth != 0 {
			return value
		}
		value = value[1 : len(value)-1]
	}
	return value
}

func parsePostgresTimestamp(value string) (time.Time, bool) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999Z07",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
