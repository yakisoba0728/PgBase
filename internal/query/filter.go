package query

import (
	"strings"

	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	maximumFilterBytes   = 4096
	maximumFilterNodes   = 100
	maximumSortKeys      = 8
	systemFieldID        = "id"
	systemFieldCreatedAt = "created_at"
	systemFieldUpdatedAt = "updated_at"
)

type filterNode interface{ isFilterNode() }

type filterComparison struct {
	field    string
	operator string
	value    filterLiteral
}

type filterAnd struct{ children []filterNode }

type filterOr struct{ children []filterNode }

func (filterComparison) isFilterNode() {}
func (filterAnd) isFilterNode()        {}
func (filterOr) isFilterNode()         {}

type filterLiteralKind int

const (
	filterLiteralString filterLiteralKind = iota
	filterLiteralNumber
	filterLiteralBool
	filterLiteralNull
)

type filterLiteral struct {
	kind filterLiteralKind
	text string
	bool bool
}

// SortKey is one ordered column of a list query. The logical field name is
// resolved against the catalog before compilation.
type SortKey struct {
	Field string
	Desc  bool
}

type filterParser struct {
	input string
	pos   int
	nodes int
}

// parseFilter parses the supported filter grammar: comparisons combined with
// "&&" and "||", grouped with parentheses. Comparisons must pair a field name
// with a literal; literal-only predicates such as "1=1" are rejected. All
// errors are reported as stable validation details without echoing input.
func parseFilter(input string) (filterNode, error) {
	if len(input) > maximumFilterBytes {
		return nil, validation("filter", "filter_too_complex")
	}
	parser := &filterParser{input: input}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.pos != len(parser.input) {
		return nil, validation("filter", "invalid_filter")
	}
	return node, nil
}

func (p *filterParser) parseOr() (filterNode, error) {
	children := []filterNode{}
	for {
		node, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, node)
		if !p.consumeKeyword("||") {
			break
		}
	}
	if len(children) == 1 {
		return children[0], nil
	}
	if err := p.countNode(); err != nil {
		return nil, err
	}
	return filterOr{children: children}, nil
}

func (p *filterParser) parseAnd() (filterNode, error) {
	children := []filterNode{}
	for {
		node, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		children = append(children, node)
		if !p.consumeKeyword("&&") {
			break
		}
	}
	if len(children) == 1 {
		return children[0], nil
	}
	if err := p.countNode(); err != nil {
		return nil, err
	}
	return filterAnd{children: children}, nil
}

func (p *filterParser) parsePrimary() (filterNode, error) {
	if err := p.countNode(); err != nil {
		return nil, err
	}
	p.skipSpaces()
	if p.consumeKeyword("(") {
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if !p.consumeKeyword(")") {
			return nil, validation("filter", "invalid_filter")
		}
		return node, nil
	}
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	p.skipSpaces()
	operator, ok := p.consumeOperator()
	if !ok {
		return nil, validation("filter", "invalid_filter")
	}
	p.skipSpaces()
	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	return p.buildComparison(left, operator, right)
}

func (p *filterParser) buildComparison(left filterOperand, operator string, right filterOperand) (filterNode, error) {
	// Comparisons pair exactly one field operand with one literal operand.
	// A literal on the left ("5 < count") flips the ordering operator so the
	// compiled SQL keeps the caller's direction.
	switch {
	case left.field != "" && right.field == "":
		return filterComparison{field: left.field, operator: operator, value: right.literal}, nil
	case left.field == "" && right.field != "":
		return filterComparison{field: right.field, operator: flipComparisonOperator(operator), value: left.literal}, nil
	default:
		return nil, validation("filter", "invalid_filter")
	}
}

func flipComparisonOperator(operator string) string {
	switch operator {
	case ">":
		return "<"
	case ">=":
		return "<="
	case "<":
		return ">"
	case "<=":
		return ">="
	default:
		return operator
	}
}

type filterOperand struct {
	field   string
	literal filterLiteral
}

func (p *filterParser) parseOperand() (filterOperand, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return filterOperand{}, validation("filter", "invalid_filter")
	}
	switch current := p.input[p.pos]; {
	case current == '\'':
		text, ok := p.consumeString()
		if !ok {
			return filterOperand{}, validation("filter", "invalid_filter")
		}
		return filterOperand{literal: filterLiteral{kind: filterLiteralString, text: text}}, nil
	case current == '-' || (current >= '0' && current <= '9'):
		number, ok := p.consumeNumber()
		if !ok {
			return filterOperand{}, validation("filter", "invalid_filter")
		}
		return filterOperand{literal: filterLiteral{kind: filterLiteralNumber, text: number}}, nil
	default:
		word, ok := p.consumeWord()
		if !ok {
			return filterOperand{}, validation("filter", "invalid_filter")
		}
		switch word {
		case "true":
			return filterOperand{literal: filterLiteral{kind: filterLiteralBool, bool: true}}, nil
		case "false":
			return filterOperand{literal: filterLiteral{kind: filterLiteralBool}}, nil
		case "null":
			return filterOperand{literal: filterLiteral{kind: filterLiteralNull}}, nil
		}
		return filterOperand{field: word}, nil
	}
}

func (p *filterParser) consumeKeyword(keyword string) bool {
	p.skipSpaces()
	if strings.HasPrefix(p.input[p.pos:], keyword) {
		p.pos += len(keyword)
		return true
	}
	return false
}

func (p *filterParser) consumeOperator() (string, bool) {
	for _, operator := range []string{"!=", ">=", "<=", "=", ">", "<"} {
		if strings.HasPrefix(p.input[p.pos:], operator) {
			p.pos += len(operator)
			return operator, true
		}
	}
	return "", false
}

func (p *filterParser) consumeString() (string, bool) {
	// p.input[p.pos] is the opening quote.
	p.pos++
	var builder strings.Builder
	for p.pos < len(p.input) {
		current := p.input[p.pos]
		if current == '\'' {
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == '\'' {
				builder.WriteByte('\'')
				p.pos += 2
				continue
			}
			p.pos++
			return builder.String(), true
		}
		builder.WriteByte(current)
		p.pos++
	}
	return "", false
}

func (p *filterParser) consumeNumber() (string, bool) {
	start := p.pos
	if p.input[p.pos] == '-' {
		p.pos++
	}
	digits := 0
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
		digits++
	}
	if digits == 0 {
		return "", false
	}
	if p.pos < len(p.input) && p.input[p.pos] == '.' {
		p.pos++
		fraction := 0
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
			fraction++
		}
		if fraction == 0 {
			return "", false
		}
	}
	if p.pos < len(p.input) && isWordByte(p.input[p.pos]) {
		return "", false
	}
	return p.input[start:p.pos], true
}

func (p *filterParser) consumeWord() (string, bool) {
	start := p.pos
	for p.pos < len(p.input) && (isWordByte(p.input[p.pos]) || p.input[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return "", false
	}
	return p.input[start:p.pos], true
}

func (p *filterParser) skipSpaces() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t' || p.input[p.pos] == '\n' || p.input[p.pos] == '\r') {
		p.pos++
	}
}

func (p *filterParser) countNode() error {
	p.nodes++
	if p.nodes > maximumFilterNodes {
		return validation("filter", "filter_too_complex")
	}
	return nil
}

func isWordByte(current byte) bool {
	return current == '_' || (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9')
}

// parseSort parses "-created_at,name" into ordered sort keys. At most
// maximumSortKeys keys are accepted and duplicates are rejected.
func parseSort(input string) ([]SortKey, error) {
	if input == "" {
		return nil, nil
	}
	if len(input) > 64*(maximumSortKeys+1) {
		return nil, validation("sort", "invalid_sort")
	}
	segments := strings.Split(input, ",")
	if len(segments) > maximumSortKeys {
		return nil, validation("sort", "invalid_sort")
	}
	keys := make([]SortKey, 0, len(segments))
	seen := make(map[string]struct{}, len(segments))
	for _, segment := range segments {
		desc := strings.HasPrefix(segment, "-")
		field := strings.TrimPrefix(segment, "-")
		if strings.HasPrefix(field, "+") {
			field = strings.TrimPrefix(field, "+")
		}
		if !validSortFieldName(field) {
			return nil, validation("sort", "invalid_sort")
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, validation("sort", "invalid_sort")
		}
		seen[field] = struct{}{}
		keys = append(keys, SortKey{Field: field, Desc: desc})
	}
	return keys, nil
}

func validSortFieldName(field string) bool {
	if len(field) == 0 {
		return false
	}
	for _, segment := range strings.Split(field, ".") {
		if !validSortFieldSegment(segment) {
			return false
		}
	}
	return true
}

// effectiveSortKeys normalizes requested keys and appends the "id" tiebreaker
// so keyset pagination has a stable total order.
// An appended tiebreaker follows the direction of the key it breaks ties for.
// A mixed order such as "created_at DESC, id ASC" matches neither scan
// direction of the (created_at, id) page index, so it costs a full sort of the
// table. Aligning it here rather than in one compiler keeps every sort entry
// point — scalar pages, relation-aware pages, root pages and expansion — on the
// same tie order, which the cursor encodes and later pages have to reproduce.
// A tiebreaker the caller wrote out itself is left exactly as requested.
func effectiveSortKeys(requested []SortKey) []SortKey {
	if len(requested) == 0 {
		return []SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}}
	}
	keys := append([]SortKey(nil), requested...)
	if keys[len(keys)-1].Field != systemFieldID {
		keys = append(keys, SortKey{Field: systemFieldID, Desc: keys[len(keys)-1].Desc})
	}
	return keys
}

// filterField resolves a logical field name to its physical column and type.
// System columns are addressable with their fixed types.
func filterField(collection schema.Collection, name string) (schema.Field, bool) {
	switch name {
	case systemFieldID:
		return schema.Field{Name: systemFieldID, ColumnName: systemFieldID, Type: schema.TypeUUID, Indexed: true}, true
	case systemFieldCreatedAt:
		return schema.Field{Name: name, ColumnName: name, Type: schema.TypeTimestampTZ, Indexed: true}, true
	case systemFieldUpdatedAt:
		return schema.Field{Name: name, ColumnName: name, Type: schema.TypeTimestampTZ}, true
	}
	for _, field := range collection.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return schema.Field{}, false
}
