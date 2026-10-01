package query

import (
	"encoding/json"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

type cursorV5FunctionArgument struct {
	Kind     string          `json:"kind"`
	Type     string          `json:"type"`
	Path     []string        `json:"path,omitempty"`
	Property string          `json:"property,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type cursorV5FunctionComparison struct {
	Kind           string                     `json:"kind"`
	Name           string                     `json:"name"`
	Arguments      []cursorV5FunctionArgument `json:"arguments"`
	Type           string                     `json:"type"`
	Operator       string                     `json:"operator"`
	Value          json.RawMessage            `json:"value"`
	PocketBaseLike bool                       `json:"pocketBaseLike,omitempty"`
}

// This is an additive descriptor shape: existing scalar descriptors and their
// signed V5 cursors retain their exact bytes. Functions previously could not
// issue a cursor because they have no scalar comparison field.
func encodeCursorV5FunctionComparison(comparison *plannedRelationComparison) (json.RawMessage, error) {
	fn := comparison.function
	arguments := make([]cursorV5FunctionArgument, len(fn.arguments))
	for index, argument := range fn.arguments {
		encoded := cursorV5FunctionArgument{Kind: "literal", Type: string(argument.typeName)}
		var err error
		if argument.field != nil {
			encoded.Kind, encoded.Property = "field", argument.property
			encoded.Path, err = cursorV5Path(*argument.field, nil)
		} else {
			encoded.Value, err = encodeCursorV5FilterValue(schema.Field{Type: argument.typeName}, argument.literal)
		}
		if err != nil {
			return nil, err
		}
		arguments[index] = encoded
	}
	value, err := encodeCursorV5FilterValue(fn.result, comparison.input.value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cursorV5FunctionComparison{
		Kind: "function-comparison", Name: fn.name, Arguments: arguments,
		Type: string(fn.result.Type), Operator: comparison.input.operator, Value: value,
		PocketBaseLike: comparison.input.pocketBaseLike,
	})
}
