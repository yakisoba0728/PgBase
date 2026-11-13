package query

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
)

const ruleAuthPrefix = "__pgbase_auth_"
const ruleAuthID = ruleAuthPrefix + "id"
const ruleRequestPrefix = "__pgbase_request_"

// ParseAccessRule reuses pgBase's bounded record-filter grammar and
// projects its dependency-free result into the persisted access-rule AST.
// Catalog field types remain enforced by accessrule.Compile before storage.
func ParseAccessRule(expression string) (accessrule.Rule, error) {
	normalized, shifts, ok := normalizeRuleReferences(expression)
	if !ok {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	node, stopped, err := parseFilterAt(normalized)
	if err != nil || node == nil {
		growth := 0
		for _, shift := range shifts {
			if shift[0] <= stopped {
				growth = shift[1]
			}
		}
		stopped = min(max(stopped-growth, 0), len(expression))
		position := strconv.Itoa(utf8.RuneCountInString(expression[:stopped]) + 1)
		return accessrule.Rule{}, errors.Join(accessrule.ErrInvalid, &ValidationError{Details: []ValidationDetail{{Field: "position", Code: position}}})
	}
	rule, err := accessRuleNode(node)
	if err != nil {
		return accessrule.Rule{}, err
	}
	source := expression
	rule.Binding = accessrule.BindingCatalog
	rule.Expression = &source
	return rule, nil
}

// ParseAccessRules is the raw policy boundary shared by HTTP, JS and import.
func ParseAccessRules(raw []byte) (accessrule.Rules, error) {
	return accessrule.ParseExpressions(raw, ParseAccessRule)
}

// The shifts record [output offset, cumulative growth] after each rewrite so a
// parser offset in the output can be mapped back to the input.
func normalizeRuleReferences(input string) (string, [][2]int, bool) {
	var out strings.Builder
	var shifts [][2]int
	for i := 0; i < len(input); {
		if growth := out.Len() - i; growth != 0 && (len(shifts) == 0 || shifts[len(shifts)-1][1] != growth) {
			shifts = append(shifts, [2]int{out.Len(), growth})
		}
		if input[i] == '"' {
			start, closed := i, false
			i++
			for i < len(input) {
				ch := input[i]
				i++
				if ch == '\\' && i < len(input) {
					i++
				} else if ch == '"' {
					closed = true
					break
				}
			}
			if !closed {
				return "", nil, false
			}
			out.WriteString(input[start:i])
			continue
		}
		if input[i] == '\'' {
			start := i
			i++
			for i < len(input) {
				if input[i] == '\'' {
					i++
					if i < len(input) && input[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			if i > len(input) || input[i-1] != '\'' {
				return "", nil, false
			}
			out.WriteString(input[start:i])
			continue
		}
		switch {
		case strings.HasPrefix(input[i:], "@request.auth."):
			i += len("@request.auth.")
			start := i
			for i < len(input) && (isWordByte(input[i]) || input[i] == '.') {
				i++
			}
			if i == start {
				return "", nil, false
			}
			out.WriteString(ruleAuthPrefix + input[start:i])
		case strings.HasPrefix(input[i:], "@request.body."):
			out.WriteString(ruleRequestPrefix + "body_")
			i += len("@request.body.")
		case strings.HasPrefix(input[i:], "@request.query."):
			out.WriteString(ruleRequestPrefix + "query_")
			i += len("@request.query.")
		case strings.HasPrefix(input[i:], "@request.headers."):
			out.WriteString(ruleRequestPrefix + "header_")
			i += len("@request.headers.")
		case strings.HasPrefix(input[i:], "@request.context."):
			out.WriteString(ruleRequestPrefix + "context_")
			i += len("@request.context.")
		case strings.HasPrefix(input[i:], "@request.method"):
			out.WriteString(ruleRequestPrefix + "method")
			i += len("@request.method")
		default:
			out.WriteByte(input[i])
			i++
		}
	}
	return out.String(), shifts, true
}

func accessRuleNode(node filterNode) (accessrule.Rule, error) {
	switch value := node.(type) {
	case filterAnd:
		args := make([]accessrule.Rule, len(value.children))
		for i, child := range value.children {
			converted, err := accessRuleNode(child)
			if err != nil {
				return accessrule.Rule{}, err
			}
			args[i] = converted
		}
		return accessrule.Rule{Op: "all", Args: args}, nil
	case filterOr:
		args := make([]accessrule.Rule, len(value.children))
		for i, child := range value.children {
			converted, err := accessRuleNode(child)
			if err != nil {
				return accessrule.Rule{}, err
			}
			args[i] = converted
		}
		return accessrule.Rule{Op: "any", Args: args}, nil
	case filterComparison:
		return accessRuleComparison(value)
	default:
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
}

func accessRuleComparison(value filterComparison) (accessrule.Rule, error) {
	op, ok := map[string]string{"=": "eq", "!=": "ne", "<": "lt", "<=": "lte", ">": "gt", ">=": "gte", "~": "contains", "!~": "not_contains", "?=": "any_eq", "?!=": "any_ne", "?<": "any_lt", "?<=": "any_lte", "?>": "any_gt", "?>=": "any_gte", "?~": "any_contains", "?!~": "any_not_contains"}[value.operator]
	if !ok {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	// A literal on the left has no representation in the persisted AST.
	if value.literalLeft {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	field := joinRuleField(value.field, value.modifier)
	var function *accessrule.Function
	if value.function != nil {
		converted, err := accessRuleFunction(value.function)
		if err != nil {
			return accessrule.Rule{}, err
		}
		function = converted
		field = "__pgbase_function"
	}
	if value.otherField != "" {
		other := joinRuleField(value.otherField, value.otherModifier)
		if strings.HasPrefix(field, ruleAuthPrefix) && strings.HasSuffix(op, "contains") {
			return accessrule.Rule{Op: op, Field: ruleRequestPrefix + "auth_" + strings.TrimPrefix(field, ruleAuthPrefix), OtherField: other}, nil
		}
		if strings.HasPrefix(field, ruleAuthPrefix) {
			field, other, op = other, field, flipRuleOp(op)
		}
		if strings.HasPrefix(other, ruleAuthPrefix) {
			return accessrule.Rule{Op: op, Field: field, Auth: strings.TrimPrefix(other, ruleAuthPrefix)}, nil
		}
		return accessrule.Rule{Op: op, Field: field, OtherField: other}, nil
	}
	if field == ruleAuthID {
		if value.value.kind != filterLiteralString || value.value.text != "" {
			return accessrule.Rule{}, accessrule.ErrInvalid
		}
		if op == "ne" {
			return accessrule.Rule{Op: "authenticated"}, nil
		}
		if op == "eq" {
			return accessrule.Rule{Op: "unauthenticated"}, nil
		}
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	if strings.HasPrefix(field, ruleAuthPrefix) {
		field = ruleRequestPrefix + "auth_" + strings.TrimPrefix(field, ruleAuthPrefix)
	}
	var literal any
	switch value.value.kind {
	case filterLiteralString:
		literal = value.value.text
	case filterLiteralNumber:
		literal = json.Number(value.value.text)
	case filterLiteralBool:
		literal = value.value.bool
	case filterLiteralNull:
		literal = nil
	default:
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	raw, err := json.Marshal(literal)
	if err != nil {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	return accessrule.Rule{Op: op, Field: field, Value: raw, Function: function, Macro: value.value.macro}, nil
}

func accessRuleFunction(function *filterFunction) (*accessrule.Function, error) {
	result := &accessrule.Function{Name: function.name, Args: make([]accessrule.FunctionArg, len(function.args))}
	for i, arg := range function.args {
		if arg.function != nil || arg.modifier != "" {
			return nil, accessrule.ErrInvalid
		}
		if arg.field != "" {
			result.Args[i].Field = arg.field
			continue
		}
		var value any
		switch arg.literal.kind {
		case filterLiteralString:
			value = arg.literal.text
		case filterLiteralNumber:
			value = json.Number(arg.literal.text)
		default:
			return nil, accessrule.ErrInvalid
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, accessrule.ErrInvalid
		}
		result.Args[i].Value = raw
		result.Args[i].Macro = arg.literal.macro
	}
	return result, nil
}

func joinRuleField(field, modifier string) string {
	if modifier == "" {
		return field
	}
	return field + ":" + modifier
}
func flipRuleOp(op string) string {
	if strings.HasPrefix(op, "any_") {
		return "any_" + flipRuleOp(strings.TrimPrefix(op, "any_"))
	}
	switch op {
	case "lt":
		return "gt"
	case "lte":
		return "gte"
	case "gt":
		return "lt"
	case "gte":
		return "lte"
	default:
		return op
	}
}
