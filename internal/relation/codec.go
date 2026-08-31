package relation

import (
	"bytes"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// Decode splits and normalizes one create or patch payload. Unknown and scalar
// members remain byte-for-byte equivalent cloned JSON for the scalar compiler.
// Relation errors contain only the field name and a stable code.
func (b *Bindings) Decode(mode InputMode, payload map[string]json.RawMessage) (Input, error) {
	if b == nil || (mode != CreateInput && mode != PatchInput) {
		return Input{}, ErrInvalidBinding
	}

	decoded := Input{
		Scalar:             make(map[string]json.RawMessage, len(payload)),
		sourceCollectionID: b.source.id,
		relations:          make([]Value, 0, len(b.fields)),
	}
	for name, raw := range payload {
		if _, relationField := b.fieldsByName[name]; relationField {
			continue
		}
		decoded.Scalar[name] = append(json.RawMessage(nil), raw...)
	}

	for _, binding := range b.fields {
		raw, present := payload[binding.field.Name]
		value := Value{
			FieldName:          binding.field.Name,
			Present:            present,
			fieldID:            binding.field.ID,
			targetCollectionID: binding.spec.TargetCollectionID,
			cardinality:        binding.spec.Cardinality,
		}
		if !present {
			if mode == CreateInput && binding.spec.MinSelect > 0 {
				return Input{}, validation(binding.field.Name, "relation_min_select")
			}
			decoded.relations = append(decoded.relations, value)
			continue
		}

		ids, clear, canonical, err := decodeValue(binding.field.Name, binding.spec, raw)
		if err != nil {
			return Input{}, err
		}
		value.IDs = ids
		value.Clear = clear
		value.JSON = canonical
		decoded.relations = append(decoded.relations, value)
		if binding.spec.Cardinality == schema.RelationSingle {
			decoded.Scalar[binding.field.Name] = append(json.RawMessage(nil), canonical...)
		}
	}
	return decoded, nil
}

func decodeValue(field string, spec schema.RelationSpec, raw json.RawMessage) ([]uuid.UUID, bool, json.RawMessage, error) {
	if spec.Cardinality == schema.RelationSingle {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if spec.MinSelect > 0 {
				return nil, false, nil, validation(field, "relation_min_select")
			}
			return nil, true, json.RawMessage("null"), nil
		}
		value, ok := jsonString(raw)
		if !ok {
			return nil, false, nil, validation(field, "relation_invalid_value")
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, false, nil, validation(field, "relation_invalid_value")
		}
		canonical, _ := json.Marshal(parsed.String())
		return []uuid.UUID{parsed}, false, canonical, nil
	}
	if spec.Cardinality != schema.RelationMultiple {
		return nil, false, nil, validation(field, "relation_invalid_value")
	}

	var members []json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return nil, false, nil, validation(field, "relation_invalid_value")
	}
	if len(members) < spec.MinSelect {
		return nil, false, nil, validation(field, "relation_min_select")
	}
	if len(members) > spec.MaxSelect {
		return nil, false, nil, validation(field, "relation_max_select")
	}

	ids := make([]uuid.UUID, 0, len(members))
	encoded := make([]string, 0, len(members))
	seen := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		value, ok := jsonString(member)
		if !ok {
			return nil, false, nil, validation(field, "relation_invalid_value")
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, false, nil, validation(field, "relation_invalid_value")
		}
		if _, duplicate := seen[parsed]; duplicate {
			return nil, false, nil, validation(field, "duplicate_relation_value")
		}
		seen[parsed] = struct{}{}
		ids = append(ids, parsed)
		encoded = append(encoded, parsed.String())
	}
	canonical, _ := json.Marshal(encoded)
	return ids, len(ids) == 0, canonical, nil
}

func jsonString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	// json.Unmarshal accepts leading/trailing whitespace but rejects trailing
	// tokens, which is the desired JSON-value contract.
	return value, true
}
