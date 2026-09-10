package query

import (
	"strings"

	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
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
	field          string
	modifier       string
	otherField     string
	otherModifier  string
	operator       string
	value          filterLiteral
	literalLeft    bool
	pocketBaseLike bool
}

type filterAnd struct{ children []filterNode }

type filterOr struct{ children []filterNode }

func (filterComparison) isFilterNode() {}
func (filterAnd) isFilterNode()        {}
func (filterOr) isFilterNode()         {}

func markPocketBaseLike(node filterNode) filterNode {
	switch value := node.(type) {
	case filterComparison:
		value.pocketBaseLike = true
		return value
	case filterAnd:
		for i := range value.children {
			value.children[i] = markPocketBaseLike(value.children[i])
		}
		return value
	case filterOr:
		for i := range value.children {
			value.children[i] = markPocketBaseLike(value.children[i])
		}
		return value
	default:
		return node
	}
}

type filterLiteralKind int

const (
	filterLiteralString filterLiteralKind = iota
	filterLiteralNumber
	filterLiteralBool
	filterLiteralNull
)

type filterLiteral struct {
	kind  filterLiteralKind
	text  string
	bool  bool
	macro string
}

type SortKey = cursor.SortKey

type filterParser struct {
	input string
	pos   int
	nodes int
}

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
	switch {
	case left.field != "" && right.field != "":
		return filterComparison{field: left.field, modifier: left.modifier, otherField: right.field, otherModifier: right.modifier, operator: operator}, nil
	case left.field != "" && right.field == "":
		return filterComparison{field: left.field, modifier: left.modifier, operator: operator, value: right.literal}, nil
	case left.field == "" && right.field != "":
		return filterComparison{field: right.field, modifier: right.modifier, operator: flipComparisonOperator(operator), value: left.literal, literalLeft: operator == "~" || operator == "!~"}, nil
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
	field, modifier string
	literal         filterLiteral
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
		field, modifier, ok := fieldvalue.SplitFilterModifier(word)
		if !ok {
			return filterOperand{}, validation("filter", "invalid_filter")
		}
		return filterOperand{field: field, modifier: modifier}, nil
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
	for _, operator := range []string{"!=", "!~", ">=", "<=", "=", ">", "<", "~"} {
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
	for p.pos < len(p.input) && (isWordByte(p.input[p.pos]) || p.input[p.pos] == '.' || p.input[p.pos] == ':') {
		p.pos++
	}
	if p.pos == start {
		return "", false
	}
	return p.input[start:p.pos], true
}

func filterTimeMacro(name string, now time.Time) (filterLiteral, bool) {
	now = now.UTC()
	date := func(v time.Time) filterLiteral {
		return filterLiteral{kind: filterLiteralString, text: v.Format(time.RFC3339Nano), macro: name}
	}
	switch name {
	case "@now":
		return date(now), true
	case "@yesterday":
		return date(now.AddDate(0, 0, -1)), true
	case "@tomorrow":
		return date(now.AddDate(0, 0, 1)), true
	case "@todayStart":
		return date(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)), true
	case "@todayEnd":
		return date(time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, int(999*time.Millisecond), time.UTC)), true
	case "@monthStart":
		return date(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)), true
	case "@monthEnd":
		return date(time.Date(now.Year(), now.Month()+1, 0, 23, 59, 59, int(999*time.Millisecond), time.UTC)), true
	case "@yearStart":
		return date(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)), true
	case "@yearEnd":
		return date(time.Date(now.Year(), 12, 31, 23, 59, 59, int(999*time.Millisecond), time.UTC)), true
	}
	numbers := map[string]int{"@second": now.Second(), "@minute": now.Minute(), "@hour": now.Hour(), "@day": now.Day(), "@month": int(now.Month()), "@weekday": int(now.Weekday()), "@year": now.Year()}
	value, ok := numbers[name]
	if !ok {
		return filterLiteral{}, false
	}
	return filterLiteral{kind: filterLiteralNumber, text: strconv.Itoa(value), macro: name}, true
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
