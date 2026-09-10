package query

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type cursorV5FilterComparison struct {
	Kind     string          `json:"kind"`
	Path     []string        `json:"path"`
	Type     string          `json:"type"`
	Modifier string          `json:"modifier,omitempty"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

type cursorV5FilterGroup struct {
	Kind     string            `json:"kind"`
	Children []json.RawMessage `json:"children"`
}

func cursorV5QueryFingerprint(codec *cursor.Codec, filter *plannedRelationFilter) ([]byte, error) {
	encodedFilter, err := encodeCursorV5Filter(filter)
	if err != nil {
		return nil, err
	}
	descriptor := append(append([]byte(`{"filter":`), encodedFilter...), '}')
	return codec.QueryFingerprint(descriptor), nil
}

func encodeCursorV5Filter(filter *plannedRelationFilter) (json.RawMessage, error) {
	if filter == nil {
		return json.RawMessage("null"), nil
	}
	if filter.comparison != nil {
		comparison := filter.comparison
		field := comparison.rootField
		var hops []plannedRelationHop
		if comparison.path != nil {
			field = &comparison.path.terminal
			hops = comparison.path.hops
		}
		if field == nil {
			return nil, schema.ErrConflict
		}
		path, err := cursorV5Path(*field, hops)
		if err != nil {
			return nil, err
		}
		value, err := encodeCursorV5FilterValue(*field, comparison.input.value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(cursorV5FilterComparison{
			Kind: "comparison", Path: path, Type: string(field.Type),
			Modifier: comparison.input.modifier, Operator: comparison.input.operator, Value: value,
		})
	}
	kind := "and"
	if filter.separator == " OR " {
		kind = "or"
	} else if filter.separator != " AND " {
		return nil, schema.ErrConflict
	}
	children := make([]json.RawMessage, 0, len(filter.children))
	for _, child := range filter.children {
		if err := appendCursorV5FilterChild(&children, child, filter.separator); err != nil {
			return nil, err
		}
	}
	sort.Slice(children, func(left, right int) bool { return bytes.Compare(children[left], children[right]) < 0 })
	return json.Marshal(cursorV5FilterGroup{Kind: kind, Children: children})
}

func appendCursorV5FilterChild(children *[]json.RawMessage, child *plannedRelationFilter, separator string) error {
	if child != nil && child.comparison == nil && child.separator == separator {
		for _, nested := range child.children {
			if err := appendCursorV5FilterChild(children, nested, separator); err != nil {
				return err
			}
		}
		return nil
	}
	encoded, err := encodeCursorV5Filter(child)
	if err == nil {
		*children = append(*children, encoded)
	}
	return err
}

func encodeCursorV5FilterValue(field schema.Field, literal filterLiteral) (json.RawMessage, error) {
	if literal.kind == filterLiteralNull {
		return json.RawMessage("null"), nil
	}
	value, err := decodeFilterValue(field, literal)
	if err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case pgtype.Numeric:
		stored, err := typed.Value()
		if err != nil {
			return nil, err
		}
		value = stored
	case time.Time:
		value = typed.UTC().Format(time.RFC3339Nano)
	case uuid.UUID:
		value = typed.String()
	}
	return json.Marshal(value)
}
