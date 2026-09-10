package query

import (
	"strconv"
	"strings"
	"time"
)

// ScalarKind is the small set of values that can be evaluated without a
// database. It lets metadata endpoints share the record-filter grammar while
// keeping their authority and data source local.
type ScalarKind uint8

const (
	ScalarString ScalarKind = iota + 1
	ScalarBool
	ScalarNumber
	ScalarTime
)

// ScalarValue is one value supplied to a compiled ScalarPredicate.
type ScalarValue struct {
	String string
	Bool   bool
	Number float64
	Time   time.Time
}

// ScalarPredicate is a parsed, validated filter expression. Evaluate is pure:
// callers provide the values, so compiling it grants no database authority.
type ScalarPredicate struct {
	node   filterNode
	fields map[string]ScalarKind
}

// CompileScalarPredicate parses the ordinary bounded PgBase/PocketBase filter
// grammar and restricts it to the supplied scalar fields. Dotted paths, field
// to field comparisons, unsupported modifiers, and incompatible literals are
// rejected during compilation.
func CompileScalarPredicate(expression string, fields map[string]ScalarKind) (ScalarPredicate, error) {
	node, err := parseFilterRequest(expression)
	if err != nil || node == nil {
		return ScalarPredicate{}, err
	}
	if err := validateScalarNode(node, fields); err != nil {
		return ScalarPredicate{}, err
	}
	copyFields := make(map[string]ScalarKind, len(fields))
	for name, kind := range fields {
		copyFields[name] = kind
	}
	return ScalarPredicate{node: node, fields: copyFields}, nil
}

// Evaluate reports whether values match the compiled predicate.
func (p ScalarPredicate) Evaluate(values map[string]ScalarValue) bool {
	return evaluateScalarNode(p.node, p.fields, values)
}

// ParseSort exposes the same bounded multi-sort parser used by record reads.
func ParseSort(input string) ([]SortKey, error) { return parseSort(input) }

func validateScalarNode(node filterNode, fields map[string]ScalarKind) error {
	switch value := node.(type) {
	case filterAnd:
		for _, child := range value.children {
			if err := validateScalarNode(child, fields); err != nil {
				return err
			}
		}
	case filterOr:
		for _, child := range value.children {
			if err := validateScalarNode(child, fields); err != nil {
				return err
			}
		}
	case filterComparison:
		kind, ok := fields[value.field]
		if !ok || strings.Contains(value.field, ".") || value.otherField != "" {
			return validation("filter", "unknown_field")
		}
		if value.modifier != "" && !(value.modifier == "lower" && kind == ScalarString) {
			return validation("filter", "invalid_filter")
		}
		if (value.operator == "~" || value.operator == "!~") && kind != ScalarString {
			return validation("filter", "invalid_filter")
		}
		if kind == ScalarBool && value.operator != "=" && value.operator != "!=" {
			return validation("filter", "invalid_filter")
		}
		if value.value.kind == filterLiteralNull || !scalarLiteralMatches(kind, value.value) {
			return validation("filter", "type_mismatch")
		}
	default:
		return validation("filter", "invalid_filter")
	}
	return nil
}

func scalarLiteralMatches(kind ScalarKind, value filterLiteral) bool {
	switch kind {
	case ScalarString:
		return value.kind == filterLiteralString
	case ScalarBool:
		return value.kind == filterLiteralBool
	case ScalarNumber:
		if value.kind != filterLiteralNumber {
			return false
		}
		_, err := strconv.ParseFloat(value.text, 64)
		return err == nil
	case ScalarTime:
		if value.kind != filterLiteralString {
			return false
		}
		parsed, err := time.Parse(time.RFC3339Nano, value.text)
		return err == nil && hasRFC3339Offset(value.text) && !parsed.IsZero()
	default:
		return false
	}
}

func evaluateScalarNode(node filterNode, fields map[string]ScalarKind, values map[string]ScalarValue) bool {
	switch value := node.(type) {
	case filterAnd:
		for _, child := range value.children {
			if !evaluateScalarNode(child, fields, values) {
				return false
			}
		}
		return true
	case filterOr:
		for _, child := range value.children {
			if evaluateScalarNode(child, fields, values) {
				return true
			}
		}
		return false
	case filterComparison:
		return evaluateScalarComparison(value, fields[value.field], values[value.field])
	default:
		return false
	}
}

func evaluateScalarComparison(comparison filterComparison, kind ScalarKind, value ScalarValue) bool {
	switch kind {
	case ScalarString:
		left, right := value.String, comparison.value.text
		if comparison.modifier == "lower" {
			left = strings.ToLower(left)
		}
		return compareScalarStrings(left, right, comparison.operator)
	case ScalarBool:
		if comparison.operator == "=" {
			return value.Bool == comparison.value.bool
		}
		return comparison.operator == "!=" && value.Bool != comparison.value.bool
	case ScalarNumber:
		right, _ := strconv.ParseFloat(comparison.value.text, 64)
		return compareScalarNumbers(value.Number, right, comparison.operator)
	case ScalarTime:
		parsed, _ := time.Parse(time.RFC3339Nano, comparison.value.text)
		return compareScalarNumbers(float64(value.Time.UnixNano()), float64(parsed.UTC().UnixNano()), comparison.operator)
	}
	return false
}

func compareScalarStrings(left, right, operator string) bool {
	switch operator {
	case "=":
		return left == right
	case "!=":
		return left != right
	case "~":
		return scalarContains(left, right)
	case "!~":
		return !scalarContains(left, right)
	case ">":
		return left > right
	case ">=":
		return left >= right
	case "<":
		return left < right
	case "<=":
		return left <= right
	}
	return false
}

func compareScalarNumbers(left, right float64, operator string) bool {
	switch operator {
	case "=":
		return left == right
	case "!=":
		return left != right
	case ">":
		return left > right
	case ">=":
		return left >= right
	case "<":
		return left < right
	case "<=":
		return left <= right
	}
	return false
}

// scalarContains keeps PocketBase's useful contains behavior while accepting
// '*' as a bounded glob wildcard. No regular expressions are compiled from
// user input.
func scalarContains(value, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return strings.Contains(value, pattern)
	}
	parts := strings.Split(pattern, "*")
	position := 0
	if parts[0] != "" {
		index := strings.Index(value, parts[0])
		if index < 0 {
			return false
		}
		position = index + len(parts[0])
	}
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		index := strings.Index(value[position:], part)
		if index < 0 {
			return false
		}
		position += index + len(part)
	}
	return true
}
