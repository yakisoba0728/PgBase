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
		kind, ok := scalarFieldKind(fields, value.field, value.modifier)
		if !ok {
			return validation("filter", "unknown_field")
		}
		if value.modifier != "" && value.modifier != "isset" && value.modifier != "changed" && !(value.modifier == "lower" && kind == ScalarString) {
			return validation("filter", "invalid_filter")
		}
		if value.otherField != "" {
			other, ok := scalarFieldKind(fields, value.otherField, value.otherModifier)
			if !ok || other != kind || (value.otherModifier != "" && value.otherModifier != "isset" && value.otherModifier != "changed" && !(value.otherModifier == "lower" && kind == ScalarString)) {
				return validation("filter", "unknown_field")
			}
			if (value.operator == "~" || value.operator == "!~") && kind != ScalarString {
				return validation("filter", "invalid_filter")
			}
			if kind == ScalarBool && value.operator != "=" && value.operator != "!=" {
				return validation("filter", "invalid_filter")
			}
			return nil
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

func scalarFieldKind(fields map[string]ScalarKind, field, modifier string) (ScalarKind, bool) {
	if modifier == "isset" || modifier == "changed" {
		kind, ok := fields[field+":"+modifier]
		return kind, ok && kind == ScalarBool
	}
	kind, ok := fields[field]
	return kind, ok
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
		field := value.field
		if value.modifier == "isset" || value.modifier == "changed" {
			field += ":" + value.modifier
		}
		otherField := value.otherField
		if value.otherModifier == "isset" || value.otherModifier == "changed" {
			otherField += ":" + value.otherModifier
		}
		if value.otherField != "" {
			return evaluateScalarFields(value, fields[field], values[field], values[otherField])
		}
		return evaluateScalarComparison(value, fields[field], values[field])
	default:
		return false
	}
}

func evaluateScalarFields(comparison filterComparison, kind ScalarKind, left, right ScalarValue) bool {
	switch kind {
	case ScalarString:
		first, second := left.String, right.String
		if comparison.modifier == "lower" {
			first = strings.ToLower(first)
		}
		if comparison.otherModifier == "lower" {
			second = strings.ToLower(second)
		}
		return compareScalarStrings(first, second, comparison.operator)
	case ScalarBool:
		if comparison.operator == "=" {
			return left.Bool == right.Bool
		}
		return comparison.operator == "!=" && left.Bool != right.Bool
	case ScalarNumber:
		return compareScalarNumbers(left.Number, right.Number, comparison.operator)
	case ScalarTime:
		return compareScalarNumbers(float64(left.Time.UnixNano()), float64(right.Time.UnixNano()), comparison.operator)
	}
	return false
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

// scalarContains mirrors PocketBase's LIKE treatment: '%' is the only explicit
// wildcard. A pattern with no unescaped '%' is escaped and wrapped in '%'.
func scalarContains(value, pattern string) bool {
	if !hasUnescapedPercent(pattern) {
		pattern = "%" + escapeLikeLiteral(pattern) + "%"
	}
	return matchLike([]rune(value), []rune(pattern))
}

func hasUnescapedPercent(value string) bool {
	escaped := false
	for _, r := range value {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '%' {
			return true
		}
	}
	return false
}
func escapeLikeLiteral(value string) string {
	var out strings.Builder
	escaped := false
	for _, r := range value {
		if !escaped && (r == '\\' || r == '%' || r == '_') {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
		escaped = !escaped && r == '\\'
		if r != '\\' {
			escaped = false
		}
	}
	return out.String()
}
func matchLike(value, pattern []rune) bool {
	var visit func(int, int) bool
	visit = func(i, j int) bool {
		for j < len(pattern) {
			if pattern[j] == '\\' && j+1 < len(pattern) {
				if i >= len(value) || value[i] != pattern[j+1] {
					return false
				}
				i++
				j += 2
				continue
			}
			if pattern[j] == '%' {
				for j < len(pattern) && pattern[j] == '%' {
					j++
				}
				if j == len(pattern) {
					return true
				}
				for k := i; k <= len(value); k++ {
					if visit(k, j) {
						return true
					}
				}
				return false
			}
			if i >= len(value) || (pattern[j] != '_' && value[i] != pattern[j]) {
				return false
			}
			i++
			j++
		}
		return i == len(value)
	}
	return visit(0, 0)
}
