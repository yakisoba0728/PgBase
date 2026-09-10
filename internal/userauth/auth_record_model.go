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

func AuthSystemFields() []schema.FieldInput {
	yes := true
	return []schema.FieldInput{
		{Name: "id", Type: schema.TypeUUID, Required: true, System: &yes}, {Name: "email", Type: schema.TypeText, Required: true, System: &yes}, {Name: "emailVisibility", Type: schema.TypeBoolean, System: &yes}, {Name: "verified", Type: schema.TypeBoolean, System: &yes}, {Name: "password", Type: schema.TypePassword, Required: true, Hidden: true, System: &yes}, {Name: "tokenKey", Type: schema.TypeText, Required: true, Hidden: true, System: &yes}, {Name: "created", Type: schema.TypeAutoDate, Required: true, System: &yes}, {Name: "updated", Type: schema.TypeAutoDate, Required: true, System: &yes},
	}
}

func normalizeAuthFields(fields []schema.FieldInput, legacy map[string]string) ([]schema.FieldInput, map[string]string, bool) {
	if fields == nil {
		for name, kind := range legacy {
			fields = append(fields, schema.FieldInput{Name: name, Type: schema.FieldType(kind)})
		}
	}
	custom := fields[:0]
	systemNames := map[string]bool{"id": true, "email": true, "emailVisibility": true, "verified": true, "password": true, "tokenKey": true, "created": true, "updated": true}
	for _, field := range fields {
		if field.System != nil && *field.System && systemNames[field.Name] {
			continue
		}
		custom = append(custom, field)
	}
	fields = custom
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
		case schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeText, schema.TypeEditor, schema.TypeTimestampTZ, schema.TypeAutoDate, schema.TypeUUID, schema.TypeJSONB, schema.TypeGeoPoint:
		case schema.TypeRelation:
			if field.Relation == nil || strings.TrimSpace(field.Relation.TargetCollection) == "" {
				return nil, nil, false
			}
			if field.Relation.Cardinality == "" {
				field.Relation.Cardinality = schema.RelationSingle
			}
			if field.Relation.OnDelete == "" {
				field.Relation.OnDelete = schema.RelationRestrict
			}
			if field.Relation.Cardinality != schema.RelationSingle && field.Relation.Cardinality != schema.RelationMultiple {
				return nil, nil, false
			}
			if field.Relation.OnDelete != schema.RelationRestrict && field.Relation.OnDelete != schema.RelationCascade && field.Relation.OnDelete != schema.RelationSetNull {
				return nil, nil, false
			}
		case schema.TypeFile:
			if field.Options == nil {
				return nil, nil, false
			}
		default:
			return nil, nil, false
		}
		if field.Type != schema.TypeRelation && field.Relation != nil || field.Unique || len(field.Default) > 0 {
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
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
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
	case schema.TypeRelation:
		if field.Relation == nil {
			return false
		}
		if field.Relation.Cardinality == schema.RelationMultiple {
			values, ok := value.([]any)
			if !ok {
				return false
			}
			min, max := 0, 1000
			if field.Relation.MinSelect != nil {
				min = *field.Relation.MinSelect
			}
			if field.Relation.MaxSelect != nil {
				max = *field.Relation.MaxSelect
			}
			if len(values) < min || len(values) > max {
				return false
			}
			seen := map[string]bool{}
			for _, raw := range values {
				v, ok := raw.(string)
				if !ok || uuid.Validate(v) != nil || seen[v] {
					return false
				}
				seen[v] = true
			}
			return true
		}
		v, ok := value.(string)
		return ok && uuid.Validate(v) == nil
	case schema.TypeFile:
		values, ok := value.([]any)
		if !ok {
			return false
		}
		min, max := 0, 1
		if field.Options != nil && field.Options.MinSelect != nil {
			min = *field.Options.MinSelect
		}
		if field.Options != nil && field.Options.MaxSelect != nil {
			max = *field.Options.MaxSelect
		}
		if len(values) < min || len(values) > max {
			return false
		}
		for _, raw := range values {
			v, ok := raw.(string)
			if !ok || uuid.Validate(v) != nil {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func applyAuthAutoDates(attributes map[string]any, fields []schema.FieldInput, create bool, now time.Time) map[string]any {
	result := make(map[string]any, len(attributes)+2)
	for k, v := range attributes {
		result[k] = v
	}
	for _, field := range fields {
		if field.Type != schema.TypeAutoDate {
			continue
		}
		if field.Options != nil && ((create && field.Options.OnCreate) || (!create && field.Options.OnUpdate)) {
			result[field.Name] = now.UTC().Format(time.RFC3339Nano)
		}
	}
	return result
}

func jsonNumber(v float64) string { raw, _ := json.Marshal(v); return string(raw) }
