package query

import (
	"encoding/json"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

type orderNullPlacement uint8

const (
	orderNullsLast orderNullPlacement = iota
	orderNullsFirst
)

// orderSpec is the single SQL and cursor contract for a root page order. It
// retains each logical key's physical type/path plus the anchor strategy, so
// rendering, continuation, privacy, cursor issuance, and reload cannot drift.
type orderSpec struct {
	root           schema.Collection
	terms          []orderTerm
	relationAnchor bool
}

type orderIndexHint uint8

const (
	orderIndexRoot orderIndexHint = iota + 1
	orderIndexRelationTarget
)

type orderTerm struct {
	key          SortKey
	expression   string
	cursorField  schema.Field
	relationPath *plannedRelationPath
	nulls        orderNullPlacement
	indexHint    orderIndexHint
	random       bool
}

func (s orderSpec) random() bool {
	for _, term := range s.terms {
		if term.random {
			return true
		}
	}
	return false
}

func indexedOrderHint(field schema.Field, hint orderIndexHint) (orderIndexHint, bool) {
	return hint, field.Indexed || field.Unique
}

func (s orderSpec) sortKeys() []SortKey {
	keys := make([]SortKey, len(s.terms))
	for index := range s.terms {
		keys[index] = s.terms[index].key
	}
	return keys
}

func (s orderSpec) private() bool {
	for index := range s.terms {
		if s.terms[index].relationPath != nil {
			return true
		}
	}
	return false
}

func (s orderSpec) recordValues(record map[string]any) ([]json.RawMessage, error) {
	return recordSortValues(s.sortKeys(), record)
}

func (s orderSpec) keyset(cursor *pageBoundary, builder *statementBuilder) (string, error) {
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
		if s.terms[index].key.Desc {
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
		if term.key.Desc {
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
		if term.relationPath != nil {
			selects = append(selects, term.cursorExpression())
		}
	}
	return selects
}

func (s orderSpec) cursorSelects() []string {
	selects := make([]string, 0, len(s.terms))
	for _, term := range s.terms {
		selects = append(selects, term.cursorExpression())
	}
	return selects
}

func (t orderTerm) cursorExpression() string {
	if t.cursorField.Type == schema.TypeNumeric {
		return t.expression + "::text"
	}
	return t.expression
}
