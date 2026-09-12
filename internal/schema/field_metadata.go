package schema

func validateFieldInputMetadata(fieldPath string, field *FieldInput) error {
	if field.Options != nil {
		options := cloneFieldOptions(*field.Options)
		field.Options = &options
	}
	if err := normalizeFieldOptionsInput(field.Options); err != nil {
		return validation(fieldPath+".options", "invalid_options")
	}
	if len([]rune(field.Help)) > 300 {
		return validation(fieldPath+".help", "too_long")
	}
	if field.Hidden && field.Presentable {
		return validation(fieldPath+".presentable", "hidden_field")
	}
	if err := validateFieldOptions(field.Type, field.Options); err != nil {
		return validation(fieldPath+".options", "invalid_options")
	}
	if !isSupportedType(field.Type) {
		return validation(fieldPath+".type", "unsupported_type")
	}
	if field.Type == TypeFile && (field.Unique || field.Indexed || len(field.Default) != 0 || field.Relation != nil) {
		return validation(fieldPath, "invalid_file_field")
	}
	if code := validateAdvancedFieldInput(*field); code != "" {
		return validation(fieldPath, code)
	}
	return nil
}
