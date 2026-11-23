package query

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
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
	// relationKeyCursor makes issued cursors anchor-only because a bare
	// relation term would otherwise carry the raw target id, which the
	// response may redact. It changes neither the SQL nor the v5 descriptor.
	relationKeyCursor bool
}

type orderIndexHint uint8

const (
	orderIndexRoot orderIndexHint = iota + 1
	orderIndexRelationTarget
)

type orderTerm struct {
	key                  SortKey
	expression           string
	cursorField          schema.Field
	relationPath         *plannedRelationPath
	nulls                orderNullPlacement
	indexHint            orderIndexHint
	random               bool
	identityCollectionID uuid.UUID
	// visibility masks a bare relation key for a principal whose relation
	// values are filtered by the target's view policy: a target the principal
	// cannot view sorts as NULL, exactly like a missing relation. It is bound
	// per principal after planning, so only a principal-bound plan carries it.
	visibility *plannedRelationHop
}

// hiddenCursor reports whether the term's cursor value is selected beside the
// record instead of being read from it, because the record does not carry the
// ordered value.
func (t orderTerm) hiddenCursor() bool {
	return t.relationPath != nil || t.identityCollectionID != uuid.Nil || t.visibility != nil
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

// sortsRelationKey reports whether a root-column term orders by a bare
// relation field. Its cursor value is the stored target id, which
// relation hydration nulls in the response when the target is not visible.
func (s orderSpec) sortsRelationKey() bool {
	for index := range s.terms {
		term := s.terms[index]
		if term.relationPath != nil || term.identityCollectionID != uuid.Nil || term.random {
			continue
		}
		if field, ok := filterField(s.root, term.key.Field); ok && field.Type == schema.TypeRelation {
			return true
		}
	}
	return false
}

// privateCursor reports whether issued cursors must omit sort values and
// carry only the keyed anchor digest. Relation-path orders always do; a bare
// relation order does when the plan was marked for a non-administrator.
// Decoding requires an anchor-only cursor whenever this holds: a value cursor
// for a masked bare relation order would carry the stored target id, which no
// longer matches the masked sort key.
func (s orderSpec) privateCursor() bool {
	return s.private() || s.relationKeyCursor
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
		if term.hiddenCursor() {
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
