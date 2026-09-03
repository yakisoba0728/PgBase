package doctor

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var physicalDecimalDefault = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

// physicalDefaultsEquivalent compares the column default the schema planner
// requires with the expression PostgreSQL deparsed from the live column. Both
// sides are canonicalized by the column's own type rather than compared as
// text, because a default written by hand, by an older planner, or under a
// different deparse style renders the same value differently. Nothing is
// executed: an expression this file cannot decode falls back to a whitespace
// insensitive text comparison, so a function-bearing default is reported as
// drift instead of being run during a read-only diagnostic.
func physicalDefaultsEquivalent(dataType, expected, observed string) bool {
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(observed) == "" {
		return strings.TrimSpace(expected) == strings.TrimSpace(observed)
	}
	canonicalExpected, expectedOK := canonicalDefaultValue(dataType, expected)
	canonicalObserved, observedOK := canonicalDefaultValue(dataType, observed)
	if expectedOK && observedOK {
		return canonicalExpected == canonicalObserved
	}
	return compactSQL(expected) == compactSQL(observed)
}

func canonicalDefaultValue(dataType, expression string) (string, bool) {
	switch dataType {
	case "boolean":
		trimmed := strings.ToLower(strings.TrimSpace(trimOuterParentheses(strings.TrimSpace(expression))))
		if trimmed == "true" || trimmed == "false" {
			return trimmed, true
		}
		return "", false
	case "integer", "bigint", "numeric":
		value, ok := numericExpressionValue(expression)
		if !ok {
			return "", false
		}
		return canonicalDecimal(value)
	case "text":
		return postgresLiteralCastValue(expression, "text")
	case "timestamp with time zone":
		if strings.EqualFold(strings.TrimSpace(expression), "CURRENT_TIMESTAMP") {
			return "current_timestamp", true
		}
		text, ok := postgresLiteralCastValue(expression, "timestamp with time zone", "timestamptz")
		if !ok {
			return "", false
		}
		value, ok := parsePostgresTimestamp(text)
		if !ok {
			return "", false
		}
		return postgresTimestampPrecision(value).UTC().Format(time.RFC3339Nano), true
	case "uuid":
		text, ok := postgresLiteralCastValue(expression, "uuid")
		if !ok {
			return "", false
		}
		value, err := uuid.Parse(text)
		if err != nil {
			return "", false
		}
		return value.String(), true
	case "jsonb":
		text, ok := postgresLiteralCastValue(expression, "jsonb")
		if !ok {
			return "", false
		}
		value, err := jsonvalue.Canonical([]byte(text))
		if err != nil {
			return "", false
		}
		return string(value), true
	default:
		return "", false
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

// postgresLiteralCastValue accepts every spelling of the same cast. The schema
// planner renders "::timestamptz" while PostgreSQL deparses the identical
// column default as "::timestamp with time zone", and neither is more correct
// than the other.
func postgresLiteralCastValue(expression string, casts ...string) (string, bool) {
	value := strings.TrimSpace(expression)
	for _, cast := range casts {
		suffix := "::" + cast
		if len(value) <= len(suffix) || !strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			continue
		}
		return parsePostgresStringLiteral(strings.TrimSpace(value[:len(value)-len(suffix)]))
	}
	return "", false
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

// parsePostgresTimestamp reads both the ISO form PostgreSQL deparses under the
// styles doctor pins and the RFC 3339 form the schema planner stores.
func parsePostgresTimestamp(value string) (time.Time, bool) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999Z07",
		time.RFC3339Nano,
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// oversizedTextDefaultCode marks a stored text default longer than the bound
// the schema input path now enforces. A database written by an older binary
// can legally hold one, and no read or write on that collection notices until
// the catalog validator applies the bound to the stored value, at which point
// the whole catalog stops loading. Reporting it read-only lets an operator
// shorten the default before upgrading instead of discovering it as a total
// outage afterwards.
const oversizedTextDefaultCode = "text_default_oversized"

// checkOversizedTextDefaults emits at most one finding, because a catalog that
// carries many oversized defaults needs the same single operator action and an
// unbounded diagnostic payload helps nobody. The message names no collection or
// field, matching every other finding in this package.
func checkOversizedTextDefaults(catalog []catalogRow, report *Report) {
	for _, row := range catalog {
		if row.fieldType == nil || *row.fieldType != string(schema.TypeText) {
			continue
		}
		if row.hasDefault == nil || !*row.hasDefault || row.defaultJSON == nil {
			continue
		}
		value, err := jsonvalue.String(json.RawMessage(*row.defaultJSON))
		if err != nil || len(value) <= schema.MaximumTextBytes {
			continue
		}
		report.Findings = append(report.Findings, finding(oversizedTextDefaultCode,
			"A stored text default is longer than this build accepts"))
		return
	}
}
