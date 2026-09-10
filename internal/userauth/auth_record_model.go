package userauth

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var authFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func normalizeAuthFields(fields []schema.FieldInput, legacy map[string]string) ([]schema.FieldInput, map[string]string, bool) {
	if fields == nil {
		for name, kind := range legacy {
			fields = append(fields, schema.FieldInput{Name: name, Type: schema.FieldType(kind)})
		}
	}
	if len(fields) > 64 {
		return nil, nil, false
	}
	seen := map[string]bool{}
	result := make([]schema.FieldInput, len(fields))
	compatibility := map[string]string{}
	for i, field := range fields {
		if field.ID != nil || !authFieldName.MatchString(field.Name) || seen[field.Name] || field.System != nil && *field.System {
			return nil, nil, false
		}
		seen[field.Name] = true
		switch field.Type {
		case schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeText, schema.TypeEditor, schema.TypeTimestampTZ, schema.TypeUUID, schema.TypeJSONB, schema.TypeGeoPoint:
		default:
			return nil, nil, false
		}
		if field.Relation != nil || field.Unique || field.Indexed || len(field.Default) > 0 {
			return nil, nil, false
		}
		result[i] = field
		compatibility[field.Name] = string(field.Type)
	}
	return result, compatibility, true
}

func validAuthRules(r AuthCollectionRules) bool {
	for _, v := range []string{r.List, r.View, r.Create, r.Update, r.Delete, r.Auth, r.Manage} {
		if len(v) > 2048 {
			return false
		}
		if v != "" && v != "true" && v != "false" && v != "@request.auth.id = id" && v != "@request.auth.id != ''" {
			return false
		}
	}
	return true
}

func ruleAllowsSelf(rule string, authenticated, target uuid.UUID) bool {
	switch strings.TrimSpace(rule) {
	case "true":
		return true
	case "@request.auth.id = id":
		return authenticated != uuid.Nil && authenticated == target
	case "@request.auth.id != ''":
		return authenticated != uuid.Nil
	default:
		return false
	}
}

// Empty preserves the pre-rule PgBase behavior for migrated collections.
func ruleAllowsOperation(rule string, authenticated, target uuid.UUID) bool {
	if strings.TrimSpace(rule) == "" {
		return true
	}
	return ruleAllowsSelf(rule, authenticated, target)
}

func validateTypedProfile(attributes map[string]any, fields []schema.FieldInput) bool {
	definitions := map[string]schema.FieldInput{}
	for _, f := range fields {
		definitions[f.Name] = f
	}
	for name, value := range attributes {
		field, ok := definitions[name]
		if !ok || !validAuthFieldValue(field, value) {
			return false
		}
	}
	for _, field := range fields {
		if field.Required {
			v, ok := attributes[field.Name]
			if !ok || v == nil {
				return false
			}
		}
	}
	raw, err := json.Marshal(attributes)
	return err == nil && len(raw) <= 64<<10
}

func validAuthFieldValue(field schema.FieldInput, value any) bool {
	o := field.Options
	options := fieldvalue.Options{}
	if o != nil {
		options = fieldvalue.Options{MinLength: o.MinLength, MaxLength: o.MaxLength, Pattern: o.Pattern, Format: o.Format, AllowedDomains: o.AllowedDomains, DeniedDomains: o.DeniedDomains, AllowedValues: o.AllowedValues, MinSelect: o.MinSelect, MaxSelect: o.MaxSelect, Min: o.Min, Max: o.Max}
	}
	switch field.Type {
	case schema.TypeBoolean:
		_, ok := value.(bool)
		return ok
	case schema.TypeInteger:
		n, ok := value.(float64)
		return ok && n == math.Trunc(n) && n >= math.MinInt32 && n <= math.MaxInt32 && fieldvalue.ValidateNumeric(strings.TrimSuffix(strings.TrimSuffix(jsonNumber(n), ".0"), "."), options) == ""
	case schema.TypeBigInt, schema.TypeNumeric:
		n, ok := value.(float64)
		return ok && fieldvalue.ValidateNumeric(jsonNumber(n), options) == ""
	case schema.TypeText, schema.TypeEditor:
		v, ok := value.(string)
		if !ok {
			return false
		}
		if field.Required && v == "" {
			return false
		}
		if field.Type == schema.TypeEditor {
			return o == nil || o.MaxSize == nil || int64(len(v)) <= *o.MaxSize
		}
		return len(v) <= 4096 && fieldvalue.ValidateText(v, options) == ""
	case schema.TypeTimestampTZ:
		v, ok := value.(string)
		if !ok {
			return false
		}
		parsed, e := time.Parse(time.RFC3339Nano, v)
		if e != nil {
			return false
		}
		if o != nil && o.Min != nil {
			b, e := time.Parse(time.RFC3339Nano, *o.Min)
			if e != nil || parsed.Before(b) {
				return false
			}
		}
		if o != nil && o.Max != nil {
			b, e := time.Parse(time.RFC3339Nano, *o.Max)
			if e != nil || parsed.After(b) {
				return false
			}
		}
		return true
	case schema.TypeUUID:
		v, ok := value.(string)
		_, e := uuid.Parse(v)
		return ok && e == nil
	case schema.TypeJSONB:
		raw, e := json.Marshal(value)
		return e == nil && (o == nil || o.MaxSize == nil || int64(len(raw)) <= *o.MaxSize)
	case schema.TypeGeoPoint:
		m, ok := value.(map[string]any)
		if !ok || len(m) != 2 {
			return false
		}
		lat, a := m["lat"].(float64)
		lon, b := m["lon"].(float64)
		return a && b && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
	default:
		return false
	}
}

func jsonNumber(v float64) string { raw, _ := json.Marshal(v); return string(raw) }
