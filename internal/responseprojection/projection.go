package responseprojection

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type ErrorCode string

const (
	ErrInvalidField    ErrorCode = "invalid_field"
	ErrDuplicateField  ErrorCode = "duplicate_field"
	ErrInvalidModifier ErrorCode = "invalid_modifier"
)

type Error struct{ Code ErrorCode }

func (e *Error) Error() string     { return string(e.Code) }
func invalid(code ErrorCode) error { return &Error{Code: code} }

const maximumProjectionFields = 100

type projectionModifier struct {
	max      int
	ellipsis bool
}
type projectionField struct {
	path     []string
	modifier *projectionModifier
}

// Projection is a compiled PocketBase-style response picker. It is schema
// independent so all response surfaces can share it after visibility filtering.
type Projection struct{ fields []projectionField }

// CompileProjection compiles comma-separated dot paths, wildcards and terminal
// :excerpt(max, withEllipsis?) modifiers. Empty input is an identity projection.
func CompileProjection(requested string) (Projection, error) {
	if requested == "" {
		return Projection{}, nil
	}
	parts, err := splitProjectionFields(requested)
	if err != nil || len(parts) > maximumProjectionFields {
		return Projection{}, invalid(ErrInvalidField)
	}
	result := Projection{fields: make([]projectionField, 0, len(parts))}
	seen := make(map[string]struct{}, len(parts))
	for _, raw := range parts {
		field, err := parseProjectionField(strings.TrimSpace(raw))
		if err != nil {
			return Projection{}, err
		}
		key := strings.Join(field.path, ".")
		if _, ok := seen[key]; ok {
			return Projection{}, invalid(ErrDuplicateField)
		}
		seen[key] = struct{}{}
		result.fields = append(result.fields, field)
	}
	return result, nil
}

func splitProjectionFields(input string) ([]string, error) {
	var parts []string
	start, depth := 0, 0
	for index, current := range input {
		switch current {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, invalid(ErrInvalidField)
			}
		case ',':
			if depth == 0 {
				parts = append(parts, input[start:index])
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, invalid(ErrInvalidField)
	}
	return append(parts, input[start:]), nil
}

func parseProjectionField(raw string) (projectionField, error) {
	if raw == "" {
		return projectionField{}, invalid(ErrInvalidField)
	}
	pathText := raw
	var modifier *projectionModifier
	if index := strings.IndexByte(raw, ':'); index >= 0 {
		pathText = raw[:index]
		expression := strings.TrimSpace(raw[index+1:])
		if !strings.HasPrefix(expression, "excerpt(") || !strings.HasSuffix(expression, ")") {
			return projectionField{}, invalid(ErrInvalidModifier)
		}
		args := strings.Split(strings.TrimSuffix(strings.TrimPrefix(expression, "excerpt("), ")"), ",")
		if len(args) < 1 || len(args) > 2 {
			return projectionField{}, invalid(ErrInvalidModifier)
		}
		max, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || max <= 0 {
			return projectionField{}, invalid(ErrInvalidModifier)
		}
		modifier = &projectionModifier{max: max}
		if len(args) == 2 {
			switch strings.ToLower(strings.TrimSpace(args[1])) {
			case "true", "t", "1", "yes", "on":
				modifier.ellipsis = true
			}
		}
	}
	path := strings.Split(strings.TrimSpace(pathText), ".")
	for _, segment := range path {
		if segment == "" || strings.ContainsAny(segment, "():,") {
			return projectionField{}, invalid(ErrInvalidField)
		}
	}
	return projectionField{path: path, modifier: modifier}, nil
}

// Apply returns a caller-owned JSON-shaped projection. Missing paths are ignored.
func (p Projection) Apply(value any) any {
	if len(p.fields) == 0 {
		return value
	}
	return applyProjection(value, p.fields)
}

func (p Projection) Empty() bool { return len(p.fields) == 0 }

func (p Projection) RootFields() []string {
	result := make([]string, len(p.fields))
	for i := range p.fields {
		result[i] = p.fields[i].path[0]
	}
	return result
}

func applyProjection(value any, fields []projectionField) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any)
		for key, child := range current {
			matches := projectionMatches(fields, key)
			if len(matches) == 0 {
				continue
			}
			terminal := false
			var nested []projectionField
			for _, match := range matches {
				if len(match.path) == 1 {
					terminal = true
					child = applyProjectionModifier(child, match.modifier)
				} else {
					next := match
					next.path = next.path[1:]
					nested = append(nested, next)
				}
			}
			if len(nested) != 0 {
				child = applyProjection(child, nested)
			} else if !terminal {
				continue
			}
			result[key] = child
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(current))
		for i := range current {
			result[i], _ = applyProjection(current[i], fields).(map[string]any)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for i := range current {
			result[i] = applyProjection(current[i], fields)
		}
		return result
	default:
		return value
	}
}

func projectionMatches(fields []projectionField, key string) []projectionField {
	result := make([]projectionField, 0, len(fields))
	for _, field := range fields {
		if field.path[0] == key || field.path[0] == "*" {
			result = append(result, field)
		}
	}
	return result
}

var (
	projectionExcludedHTML = regexp.MustCompile(`(?is)<(?:script|style|head|iframe|embed|applet|object|svg|picture|dialog|template|button|form|textarea|select|option)\b[^>]*>.*?</(?:script|style|head|iframe|embed|applet|object|svg|picture|dialog|template|button|form|textarea|select|option)\s*>|<(?:img|input|embed)\b[^>]*?/?>`)
	projectionHTMLTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	projectionWhitespace   = regexp.MustCompile(`\s+`)
)

func applyProjectionModifier(value any, modifier *projectionModifier) any {
	if modifier == nil {
		return value
	}
	text, ok := value.(string)
	if !ok {
		return value
	}
	text = projectionExcludedHTML.ReplaceAllString(text, " ")
	text = projectionHTMLTag.ReplaceAllString(text, " ")
	text = strings.TrimSpace(projectionWhitespace.ReplaceAllString(html.UnescapeString(text), " "))
	runes := []rune(text)
	if len(runes) <= modifier.max {
		return text
	}
	text = strings.TrimRightFunc(string(runes[:modifier.max]), unicode.IsSpace)
	if modifier.ellipsis {
		text += "..."
	}
	return text
}
