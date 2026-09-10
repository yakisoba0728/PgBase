package query

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s orderSpec) cursorV5Order() ([]cursor.V5OrderTerm, error) {
	terms := make([]cursor.V5OrderTerm, len(s.terms))
	for index, term := range s.terms {
		var hops []plannedRelationHop
		if term.relationPath != nil {
			hops = term.relationPath.hops
		}
		path, err := cursorV5Path(term.cursorField, hops)
		if err != nil {
			return nil, err
		}
		nulls := cursor.NullsLast
		if term.nulls == orderNullsFirst {
			nulls = cursor.NullsFirst
		}
		terms[index] = cursor.V5OrderTerm{
			Path: path, Type: string(term.cursorField.Type), Desc: term.key.Desc, Nulls: nulls,
		}
	}
	if !cursor.ValidateV5Order(terms) {
		return nil, schema.ErrConflict
	}
	return terms, nil
}

func cursorV5Path(field schema.Field, hops []plannedRelationHop) ([]string, error) {
	path := make([]string, 0, len(hops)+1)
	for _, hop := range hops {
		if hop.field.ID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		path = append(path, hop.field.ID.String())
	}
	identity := field.ID.String()
	switch field.Name {
	case systemFieldID:
		identity = "$id"
	case systemFieldCreatedAt:
		identity = "$created_at"
	case systemFieldUpdatedAt:
		identity = "$updated_at"
	default:
		if field.ID == uuid.Nil {
			return nil, schema.ErrConflict
		}
	}
	return append(path, identity), nil
}
