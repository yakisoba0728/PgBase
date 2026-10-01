package query

import (
	"strings"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func planFieldComparison(root schema.Collection, comparison *plannedRelationComparison) error {
	input := comparison.input
	other, found := filterField(root, input.otherField)
	if !found || other.Type == schema.TypeRelation || strings.Contains(input.otherField, ".") {
		return validation("filter", "unknown_field")
	}
	other, identityCollectionID := textIdentityField(root, other)
	left := comparison.rootField
	if err := validateFieldComparison(*left, other, input); err != nil {
		return err
	}
	comparison.otherRootField = &other
	comparison.otherRootIdentityCollectionID = identityCollectionID
	return nil
}

func validateFieldComparison(left, right schema.Field, comparison filterComparison) error {
	switch comparison.operator {
	case "=", "!=", "<", "<=", ">", ">=", "~", "!~":
	default:
		return validation("filter", "invalid_filter")
	}
	if left.Type != right.Type {
		return validation("filter", "type_mismatch")
	}
	leftType, err := fieldComparisonType(left.Type, comparison.modifier)
	if err != nil {
		return err
	}
	rightType, err := fieldComparisonType(right.Type, comparison.otherModifier)
	if err != nil {
		return err
	}
	if leftType != rightType {
		return validation("filter", "type_mismatch")
	}
	if comparison.operator == "~" || comparison.operator == "!~" {
		if leftType != schema.TypeText && leftType != schema.TypeEditor {
			return validation("filter", "type_mismatch")
		}
	}
	if leftType == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=" {
		return validation("filter", "type_mismatch")
	}
	return nil
}

func fieldComparisonType(fieldType schema.FieldType, modifier string) (schema.FieldType, error) {
	switch fieldType {
	case schema.TypeText, schema.TypeEditor, schema.TypeBoolean, schema.TypeInteger,
		schema.TypeBigInt, schema.TypeNumeric, schema.TypeUUID, schema.TypeTimestampTZ, schema.TypeAutoDate:
	default:
		return "", validation("filter", "type_mismatch")
	}
	if modifier == "" {
		return fieldType, nil
	}
	if fieldType == schema.TypeText || fieldType == schema.TypeEditor {
		switch modifier {
		case "lower":
			return fieldType, nil
		case "length":
			return schema.TypeInteger, nil
		}
	}
	return "", validation("filter", "unsupported_modifier")
}
