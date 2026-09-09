package schema

func isSupportedType(typ FieldType) bool {
	switch typ {
	case TypeBoolean, TypeInteger, TypeBigInt, TypeNumeric, TypeText, TypeEditor, TypeGeoPoint, TypeAutoDate, TypePassword, TypeTimestampTZ, TypeUUID, TypeJSONB, TypeRelation, TypeFile:
		return true
	default:
		return false
	}
}

func isSupportedTypeChange(from, to FieldType) bool {
	return (from == TypeInteger && to == TypeBigInt) ||
		((from == TypeInteger || from == TypeBigInt) && to == TypeNumeric)
}

func physicalFieldType(typ FieldType) string {
	switch typ {
	case TypeEditor, TypePassword:
		return "text"
	case TypeGeoPoint:
		return "jsonb"
	case TypeAutoDate:
		return "timestamptz"
	default:
		return string(typ)
	}
}
