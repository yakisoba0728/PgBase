package schema

func isSupportedType(typ FieldType) bool {
	switch typ {
	case TypeBoolean, TypeInteger, TypeBigInt, TypeNumeric, TypeText, TypeTimestampTZ, TypeUUID, TypeJSONB, TypeRelation, TypeFile:
		return true
	default:
		return false
	}
}

func isSupportedTypeChange(from, to FieldType) bool {
	return (from == TypeInteger && to == TypeBigInt) ||
		((from == TypeInteger || from == TypeBigInt) && to == TypeNumeric)
}
