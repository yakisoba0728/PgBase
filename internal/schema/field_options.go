package schema

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
)

func unchangedStoredDefault(previous PlannedField, desired FieldInput) json.RawMessage {
	if previous.Type != desired.Type || len(previous.Default) == 0 || len(desired.Default) == 0 {
		return nil
	}
	canonical, _, err := normalizeStoredDefault(desired.Type, desired.Default)
	if err != nil || !bytes.Equal(previous.Default, canonical) {
		return nil
	}
	return previous.Default
}
func sameCanonicalDefault(previous, desired json.RawMessage) bool {
	return bytes.Equal(previous, desired)
}

func defaultSatisfiesOptions(typ FieldType, raw json.RawMessage, options FieldOptions) bool {
	if typ == TypeText {
		value, err := jsonvalue.String(raw)
		return err == nil && fieldvalue.ValidateText(value, fieldValueOptions(options)) == ""
	}
	if typ == TypeJSONB && len(options.AllowedValues) > 0 {
		var values []string
		return json.Unmarshal(raw, &values) == nil && values != nil && fieldvalue.ValidateSelection(values, fieldValueOptions(options)) == ""
	}
	if typ != TypeInteger && typ != TypeBigInt && typ != TypeNumeric {
		return true
	}
	value, err := jsonvalue.String(raw)
	if err != nil {
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return false
		}
		value = number.String()
	}
	if typ == TypeInteger {
		if _, err = strconv.ParseInt(value, 10, 32); err != nil {
			return false
		}
	}
	return fieldvalue.ValidateNumeric(value, fieldValueOptions(options)) == ""
}
func fieldValueOptions(o FieldOptions) fieldvalue.Options {
	return fieldvalue.Options{MinLength: o.MinLength, MaxLength: o.MaxLength, MinSelect: o.MinSelect, MaxSelect: o.MaxSelect, Pattern: o.Pattern, Format: o.Format, AllowedDomains: o.AllowedDomains, DeniedDomains: o.DeniedDomains, AutogeneratePattern: o.AutogeneratePattern, AllowedValues: o.AllowedValues, Min: o.Min, Max: o.Max}
}
func fieldInputValueOptions(o *FieldOptions) fieldvalue.Options {
	if o == nil {
		return fieldvalue.Options{}
	}
	return fieldValueOptions(*o)
}
func clonedInputOptions(o *FieldOptions) FieldOptions {
	if o == nil {
		return FieldOptions{}
	}
	return cloneFieldOptions(*o)
}

func normalizeFieldOptionsInput(o *FieldOptions) error {
	if o == nil {
		return nil
	}
	allowed, err := fieldvalue.NormalizeDomains(o.AllowedDomains)
	if err != nil {
		return err
	}
	denied, err := fieldvalue.NormalizeDomains(o.DeniedDomains)
	if err != nil {
		return err
	}
	o.AllowedDomains = allowed
	o.DeniedDomains = denied
	return nil
}
func relationStorageName(collection string, field FieldInput) string {
	if field.Relation != nil && field.Relation.Cardinality == RelationMultiple {
		return NewRelationPhysicalNames(collection, field.Name).MultiTable
	}
	return field.Name
}
func collectionNameByID(catalog Catalog, id uuid.UUID) (string, bool) {
	if id == uuid.Nil {
		return "", false
	}
	for name, collection := range catalog.Collections {
		if collection.ID == id {
			return name, true
		}
	}
	return "", false
}
func collectionHasIncomingRelation(catalog Catalog, id uuid.UUID) bool {
	for _, relation := range catalog.Incoming[id] {
		if relation.SourceCollectionID != id {
			return true
		}
	}
	return false
}
