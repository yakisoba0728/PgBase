package query

import (
	"encoding/json"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
)

const pocketBaseAuthPrefix = "__pgbase_pb_auth_"
const pocketBaseAuthID = pocketBaseAuthPrefix + "id"
const pocketBaseRequestPrefix = "__pgbase_request_"

// ParsePocketBaseAccessRule reuses the bounded record-filter grammar and
// projects its dependency-free result into the persisted access-rule AST.
// Catalog field types remain enforced by accessrule.Compile before storage.
func ParsePocketBaseAccessRule(expression string) (accessrule.Rule, error) {
	normalized, ok := normalizePocketBaseRuleReferences(expression)
	if !ok {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	node, err := parseFilterRequest(normalized)
	if err != nil || node == nil {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	rule, err := accessRuleNode(node)
	if err != nil {
		return accessrule.Rule{}, err
	}
	source := expression
	rule.PocketBase = &source
	return rule, nil
}

func normalizePocketBaseRuleReferences(input string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(input); {
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
				return "", false
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
				return "", false
			}
			out.WriteString(pocketBaseAuthPrefix + input[start:i])
		case strings.HasPrefix(input[i:], "@request.body."):
			out.WriteString(pocketBaseRequestPrefix + "body_")
			i += len("@request.body.")
		case strings.HasPrefix(input[i:], "@request.query."):
			out.WriteString(pocketBaseRequestPrefix + "query_")
			i += len("@request.query.")
		case strings.HasPrefix(input[i:], "@request.headers."):
			out.WriteString(pocketBaseRequestPrefix + "header_")
			i += len("@request.headers.")
		case strings.HasPrefix(input[i:], "@request.context."):
			out.WriteString(pocketBaseRequestPrefix + "context_")
			i += len("@request.context.")
		case strings.HasPrefix(input[i:], "@request.method"):
			out.WriteString(pocketBaseRequestPrefix + "method")
			i += len("@request.method")
		default:
			out.WriteByte(input[i])
			i++
		}
	}
	return out.String(), true
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
	op, ok := map[string]string{"=": "eq", "!=": "ne", "<": "lt", "<=": "lte", ">": "gt", ">=": "gte"}[value.operator]
	if !ok {
		return accessrule.Rule{}, accessrule.ErrInvalid
	}
	field := joinRuleField(value.field, value.modifier)
	if value.otherField != "" {
		other := joinRuleField(value.otherField, value.otherModifier)
		if strings.HasPrefix(field, pocketBaseAuthPrefix) {
			field, other, op = other, field, flipRuleOp(op)
		}
		if strings.HasPrefix(other, pocketBaseAuthPrefix) {
			return accessrule.Rule{Op: op, Field: field, Auth: strings.TrimPrefix(other, pocketBaseAuthPrefix)}, nil
		}
		return accessrule.Rule{Op: op, Field: field, OtherField: other}, nil
	}
	if field == pocketBaseAuthID {
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
	if strings.HasPrefix(field, pocketBaseAuthPrefix) {
		field = pocketBaseRequestPrefix + "auth_" + strings.TrimPrefix(field, pocketBaseAuthPrefix)
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
	return accessrule.Rule{Op: op, Field: field, Value: raw}, nil
}

func joinRuleField(field, modifier string) string {
	if modifier == "" {
		return field
	}
	return field + ":" + modifier
}
func flipRuleOp(op string) string {
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
