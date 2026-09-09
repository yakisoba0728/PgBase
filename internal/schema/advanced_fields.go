package schema

import "encoding/json"

func validateAdvancedFieldInput(field FieldInput) string {
	switch field.Type {
	case TypeGeoPoint:
		if field.Unique || !validAdvancedFixedDefault(field) || field.Relation != nil {
			return "invalid_geo_point_field"
		}
	case TypeEditor:
		if !validAdvancedFixedDefault(field) || field.Relation != nil || (field.Unique || field.Indexed) && editorMaximumSize(field.Options) > MaximumTextBytes {
			return "invalid_editor_field"
		}
	case TypeAutoDate:
		if field.Required || field.Unique || len(field.Default) != 0 || field.Relation != nil {
			return "invalid_autodate_field"
		}
	case TypePassword:
		if field.Unique || field.Indexed || field.Presentable || len(field.Default) != 0 || field.Relation != nil {
			return "invalid_password_field"
		}
	}
	return ""
}

func physicalFieldNotNull(field PlannedField) bool {
	return field.Required || field.Type == TypeEditor || field.Type == TypeGeoPoint
}

func validAdvancedFixedDefault(field FieldInput) bool {
	if len(field.Default) == 0 {
		return true
	}
	switch field.Type {
	case TypeEditor:
		var value string
		return json.Unmarshal(field.Default, &value) == nil && value == ""
	case TypeGeoPoint:
		var value map[string]float64
		if json.Unmarshal(field.Default, &value) != nil || len(value) != 2 {
			return false
		}
		lat, hasLat := value["lat"]
		lon, hasLon := value["lon"]
		return hasLat && hasLon && lat == 0 && lon == 0
	default:
		return false
	}
}
