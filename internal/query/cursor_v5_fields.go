package query

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type cursorV5FieldOperand struct {
	Path               []string `json:"path"`
	Type               string   `json:"type"`
	Modifier           string   `json:"modifier,omitempty"`
	IdentityCollection string   `json:"identityCollection,omitempty"`
}

type cursorV5FieldComparison struct {
	Kind     string               `json:"kind"`
	Left     cursorV5FieldOperand `json:"left"`
	Right    cursorV5FieldOperand `json:"right"`
	Operator string               `json:"operator"`
}

// Field comparisons cannot share a literal descriptor: the old descriptor
// silently treated the missing RHS literal as an empty string. There is no
// legacy-hash fallback because it cannot establish which RHS was authorized.
func encodeCursorV5FieldComparison(comparison *plannedRelationComparison) (json.RawMessage, error) {
	left, err := cursorV5ComparisonOperand(comparison.rootField, comparison.input.modifier, comparison.rootIdentityCollectionID)
	if err != nil {
		return nil, err
	}
	right, err := cursorV5ComparisonOperand(comparison.otherRootField, comparison.input.otherModifier, comparison.otherRootIdentityCollectionID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cursorV5FieldComparison{Kind: "field-comparison", Left: left, Right: right, Operator: comparison.input.operator})
}

func cursorV5ComparisonOperand(field *schema.Field, modifier string, identityCollection uuid.UUID) (cursorV5FieldOperand, error) {
	if field == nil {
		return cursorV5FieldOperand{}, schema.ErrConflict
	}
	path, err := cursorV5Path(*field, nil)
	operand := cursorV5FieldOperand{Path: path, Type: string(field.Type), Modifier: modifier}
	if identityCollection != uuid.Nil {
		operand.IdentityCollection = identityCollection.String()
	}
	return operand, err
}
