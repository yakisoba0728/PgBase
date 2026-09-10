package recordops

import (
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type inputModifier uint8

const (
	modifierNone inputModifier = iota
	modifierAppend
	modifierPrepend
	modifierRemove
)

func splitInputModifier(key string) (string, inputModifier) {
	if strings.HasPrefix(key, "+") && len(key) > 1 {
		return key[1:], modifierPrepend
	}
	if strings.HasSuffix(key, "+") && len(key) > 1 {
		return key[:len(key)-1], modifierAppend
	}
	if strings.HasSuffix(key, "-") && len(key) > 1 {
		return key[:len(key)-1], modifierRemove
	}
	return key, modifierNone
}

func resolveInputModifiers(collection schema.Collection, input map[string]json.RawMessage, before map[string]any) (map[string]json.RawMessage, error) {
	fields := make(map[string]schema.Field, len(collection.Fields))
	for _, field := range collection.Fields {
		fields[field.Name] = field
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) < len(keys[j])
		}
		return keys[i] < keys[j]
	})
	resolved := make(map[string]json.RawMessage, len(input))
	current := cloneRecordValues(before)
	for _, key := range keys {
		name, modifier := splitInputModifier(key)
		field, ok := fields[name]
		if !ok {
			return nil, modifierValidation(key, "unknown_field")
		}
		if modifier == modifierNone {
			resolved[name] = append(json.RawMessage(nil), input[key]...)
			var direct any
			if err := json.Unmarshal(input[key], &direct); err != nil {
				return nil, modifierValidation(name, "type_mismatch")
			}
			current[name] = direct
			continue
		}
		value, err := applyInputModifier(field, current[name], input[key], modifier)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, modifierValidation(name, "type_mismatch")
		}
		resolved[name], current[name] = raw, value
	}
	return resolved, nil
}

func validateModifierInput(collection schema.Collection, input map[string]json.RawMessage) error {
	directFields := make(map[string]bool)
	for key := range input {
		name, modifier := splitInputModifier(key)
		if modifier == modifierNone {
			directFields[name] = true
		}
	}
	resolved, err := resolveInputModifiers(collection, input, modifierCreateValues(collection))
	if err != nil {
		return err
	}
	// A direct assignment fixes the field's preimage, so its full modifier chain
	// can be validated before the row read. Modifier-only constraints depend on
	// the actual stored value and are validated after the row is locked.
	determined := make(map[string]json.RawMessage, len(directFields))
	for name := range directFields {
		for _, field := range collection.Fields {
			if field.Name == name && field.Relation == nil {
				determined[name] = resolved[name]
				break
			}
		}
	}
	if len(determined) == 0 {
		return nil
	}
	_, err = query.CompileUpdate(collection, uuid.New(), determined)
	return err
}

func applyInputModifier(field schema.Field, current any, raw json.RawMessage, modifier inputModifier) (any, error) {
	if field.Type == schema.TypeInteger || field.Type == schema.TypeBigInt || field.Type == schema.TypeNumeric {
		if modifier == modifierPrepend {
			return nil, modifierValidation(field.Name, "unsupported_modifier")
		}
		return modifyNumber(field, current, raw, modifier)
	}
	if field.Type == schema.TypeRelation && field.Relation != nil || len(field.Options.AllowedValues) > 0 {
		return modifyStringSelection(field, current, raw, modifier)
	}
	return nil, modifierValidation(field.Name, "unsupported_modifier")
}

func modifyNumber(field schema.Field, current any, raw json.RawMessage, modifier inputModifier) (any, error) {
	left := numericText(current)
	right := ""
	if field.Type == schema.TypeInteger {
		var value int64
		if json.Unmarshal(raw, &value) != nil || value < -2147483648 || value > 2147483647 {
			return nil, modifierValidation(field.Name, "type_mismatch")
		}
		right = strconv.FormatInt(value, 10)
	} else if json.Unmarshal(raw, &right) != nil {
		return nil, modifierValidation(field.Name, "type_mismatch")
	}
	a, okA := new(big.Rat).SetString(left)
	b, okB := new(big.Rat).SetString(right)
	if !okA || !okB {
		return nil, modifierValidation(field.Name, "type_mismatch")
	}
	if modifier == modifierRemove {
		a.Sub(a, b)
	} else {
		a.Add(a, b)
	}
	if field.Type == schema.TypeInteger || field.Type == schema.TypeBigInt {
		if !a.IsInt() || !a.Num().IsInt64() {
			return nil, modifierValidation(field.Name, "type_mismatch")
		}
		value := a.Num().Int64()
		if field.Type == schema.TypeInteger {
			if value < -2147483648 || value > 2147483647 {
				return nil, modifierValidation(field.Name, "type_mismatch")
			}
			return value, nil
		}
		return strconv.FormatInt(value, 10), nil
	}
	scale := max(decimalScale(left), decimalScale(right))
	return a.FloatString(scale), nil
}

func numericText(value any) string {
	switch value := value.(type) {
	case nil:
		return "0"
	case string:
		return value
	case int32:
		return strconv.FormatInt(int64(value), 10)
	case int64:
		return strconv.FormatInt(value, 10)
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func modifierCreateValues(collection schema.Collection) map[string]any {
	values := make(map[string]any, len(collection.Fields))
	for _, field := range collection.Fields {
		if len(field.Default) != 0 {
			var value any
			if json.Unmarshal(field.Default, &value) == nil {
				values[field.Name] = value
				continue
			}
		}
		switch field.Type {
		case schema.TypeInteger:
			values[field.Name] = int32(0)
		case schema.TypeBigInt, schema.TypeNumeric:
			values[field.Name] = "0"
		case schema.TypeText, schema.TypeEditor:
			values[field.Name] = ""
		case schema.TypeJSONB:
			if len(field.Options.AllowedValues) > 0 {
				values[field.Name] = []string{}
			}
		case schema.TypeRelation:
			if field.Relation != nil && field.Relation.Cardinality == schema.RelationMultiple {
				values[field.Name] = []string{}
			} else {
				values[field.Name] = ""
			}
		}
	}
	return values
}

func decimalScale(value string) int {
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		return len(value) - dot - 1
	}
	return 0
}

func modifyStringSelection(field schema.Field, current any, raw json.RawMessage, modifier inputModifier) (any, error) {
	existing, ok := stringValues(current)
	if !ok {
		return nil, modifierValidation(field.Name, "type_mismatch")
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, modifierValidation(field.Name, "type_mismatch")
	}
	changed, ok := stringValues(value)
	if !ok {
		return nil, modifierValidation(field.Name, "type_mismatch")
	}
	if modifier == modifierRemove {
		removed := make(map[string]bool, len(changed))
		for _, item := range changed {
			removed[item] = true
		}
		kept := existing[:0]
		for _, item := range existing {
			if !removed[item] {
				kept = append(kept, item)
			}
		}
		existing = kept
	} else if modifier == modifierPrepend {
		existing = append(changed, existing...)
	} else {
		existing = append(existing, changed...)
	}
	existing = uniqueStrings(existing)
	if field.Type == schema.TypeRelation && field.Relation.Cardinality == schema.RelationSingle || field.Type == schema.TypeText {
		if len(existing) == 0 {
			return "", nil
		}
		return existing[len(existing)-1], nil
	}
	return existing, nil
}

func stringValues(value any) ([]string, bool) {
	switch value := value.(type) {
	case nil:
		return []string{}, true
	case string:
		if value == "" {
			return []string{}, true
		}
		return []string{value}, true
	case []string:
		return append([]string(nil), value...), true
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(value, &decoded) != nil {
			return nil, false
		}
		return stringValues(decoded)
	case []any:
		result := make([]string, len(value))
		for index, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[index] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func cloneRecordValues(record map[string]any) map[string]any {
	result := make(map[string]any, len(record))
	for key, value := range record {
		result[key] = value
	}
	return result
}

func modifierValidation(field, code string) error {
	return &query.ValidationError{Details: []query.ValidationDetail{{Field: field, Code: code}}}
}
