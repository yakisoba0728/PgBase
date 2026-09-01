package query

import (
	"strings"

	"github.com/webisnoteasy/PgBase/internal/schema"
)

type orderNullPlacement uint8

const (
	orderNullsLast orderNullPlacement = iota
	orderNullsFirst
)

// orderSpec is the single SQL and cursor contract for a root page order.
// Callers only construct terms; null-safe continuation and ORDER BY rendering
// stay coupled here so a placement change cannot make the two disagree.
type orderSpec struct {
	terms []orderTerm
}

type orderTerm struct {
	expression        string
	cursorExpression  string
	cursorField       schema.Field
	descending        bool
	nulls             orderNullPlacement
	hiddenCursorValue bool
}

func (s orderSpec) keyset(cursor *PageCursor, builder *statementBuilder) (string, error) {
	if cursor == nil || len(cursor.Values) != len(s.terms) {
		return "", invalidCursor()
	}
	placeholders := make([]string, len(s.terms))
	for index, raw := range cursor.Values {
		value, err := decodeCursorValue(s.terms[index].cursorField, raw)
		if err != nil {
			return "", err
		}
		placeholders[index] = builder.placeholder(value)
	}

	disjunctions := make([]string, 0, len(s.terms))
	for index := range s.terms {
		conjuncts := make([]string, 0, index+1)
		for prior := 0; prior < index; prior++ {
			conjuncts = append(conjuncts, "("+s.terms[prior].expression+" IS NOT DISTINCT FROM "+placeholders[prior]+")")
		}
		operator := ">"
		if s.terms[index].descending {
			operator = "<"
		}
		expression := s.terms[index].expression
		boundary := "(" + placeholders[index] + " IS NOT NULL AND " + expression + " IS NULL)"
		if s.terms[index].nulls == orderNullsFirst {
			boundary = "(" + placeholders[index] + " IS NULL AND " + expression + " IS NOT NULL)"
		}
		conjuncts = append(conjuncts, "("+expression+" "+operator+" "+placeholders[index]+" OR "+boundary+")")
		conjunction := strings.Join(conjuncts, " AND ")
		if len(conjuncts) > 1 {
			conjunction = "(" + conjunction + ")"
		}
		disjunctions = append(disjunctions, conjunction)
	}
	return "(" + strings.Join(disjunctions, " OR ") + ")", nil
}

func (s orderSpec) orderBy() string {
	segments := make([]string, 0, len(s.terms))
	for _, term := range s.terms {
		direction := "ASC"
		if term.descending {
			direction = "DESC"
		}
		nulls := "NULLS LAST"
		if term.nulls == orderNullsFirst {
			nulls = "NULLS FIRST"
		}
		segments = append(segments, term.expression+" "+direction+" "+nulls)
	}
	return " ORDER BY " + strings.Join(segments, ", ")
}

func (s orderSpec) appendHiddenCursorSelects(selects []string) []string {
	for _, term := range s.terms {
		if term.hiddenCursorValue {
			selects = append(selects, term.cursorExpression)
		}
	}
	return selects
}

func (s orderSpec) cursorSelects() []string {
	selects := make([]string, 0, len(s.terms))
	for _, term := range s.terms {
		selects = append(selects, term.cursorExpression)
	}
	return selects
}

func orderCursorExpression(expression string, field schema.Field) string {
	if field.Type == schema.TypeNumeric {
		return expression + "::text"
	}
	return expression
}
