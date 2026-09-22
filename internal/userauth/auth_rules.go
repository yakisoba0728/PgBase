package userauth

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type AuthRuleContext struct {
	Principal identity.Subject
	AuthEmail string
	Current   AuthRecord
	Request   map[string]any
}

func EvaluateAuthRecordRule(rule string, fields []schema.FieldInput, context AuthRuleContext) bool {
	if context.Principal.Kind == identity.Administrator {
		return true
	}
	switch strings.TrimSpace(rule) {
	case "":
		return true
	case "true":
		return true
	case "false":
		return false
	}
	kinds, values := authRuleScalars(fields, context)
	predicate, err := query.CompileScalarPredicate(normalizeAuthRule(rule), kinds)
	return err == nil && predicate.Evaluate(values)
}

func ValidateAuthRecordRules(rules AuthCollectionRules, fields []schema.FieldInput) bool {
	for _, rule := range []string{rules.List, rules.View, rules.Create, rules.Update, rules.Delete, rules.Auth, rules.Manage} {
		if len(rule) > 2048 {
			return false
		}
		trimmed := strings.TrimSpace(rule)
		if trimmed == "" || trimmed == "true" || trimmed == "false" {
			continue
		}
		parsed, err := query.ParsePocketBaseAccessRule(rule)
		if err != nil || !validateAuthRuleFields(parsed, fields) {
			return false
		}
	}
	return true
}

func validateAuthRuleFields(rule accessrule.Rule, fields []schema.FieldInput) bool {
	if rule.Op == "all" || rule.Op == "any" {
		for _, child := range rule.Args {
			if !validateAuthRuleFields(child, fields) {
				return false
			}
		}
		return true
	}
	valid := func(path string) bool {
		if path == "" || strings.HasPrefix(path, "__pgbase_request_") || strings.HasPrefix(path, "@collection.") {
			return true
		}
		root := strings.SplitN(path, ".", 2)[0]
		for _, system := range []string{"id", "email", "emailVisibility", "password", "verified", "active", "created", "updated"} {
			if root == system {
				return !strings.Contains(path, ".")
			}
		}
		_, ok := authRuleField(fields, root)
		return ok
	}
	return valid(rule.Field) && valid(rule.OtherField)
}

func authRuleScalars(fields []schema.FieldInput, context AuthRuleContext) (map[string]query.ScalarKind, map[string]query.ScalarValue) {
	kinds := map[string]query.ScalarKind{
		"id": query.ScalarString, "email": query.ScalarString, "emailVisibility": query.ScalarBool, "verified": query.ScalarBool, "active": query.ScalarBool,
		"created": query.ScalarTime, "updated": query.ScalarTime,
		"request.auth.id": query.ScalarString, "request.auth.email": query.ScalarString,
	}
	values := map[string]query.ScalarValue{
		"id": {String: context.Current.ID.String()}, "email": {String: context.Current.Email}, "emailVisibility": {Bool: context.Current.EmailVisibility},
		"verified": {Bool: context.Current.Verified}, "active": {Bool: context.Current.Active},
		"created": {Time: context.Current.CreatedAt}, "updated": {Time: context.Current.UpdatedAt},
		"request.auth.email": {String: context.AuthEmail},
	}
	for name, kind := range map[string]query.ScalarKind{"id": query.ScalarString, "email": query.ScalarString, "emailVisibility": query.ScalarBool, "password": query.ScalarString, "verified": query.ScalarBool, "active": query.ScalarBool} {
		requestName := "request.body." + name
		kinds[requestName] = kind
		values[requestName] = authRuleScalarValue(kind, context.Request[name])
		for _, modifier := range []string{"isset", "changed"} {
			key := requestName + ":" + modifier
			kinds[key] = query.ScalarBool
			requested, present := context.Request[name]
			current := map[string]any{"id": context.Current.ID.String(), "email": context.Current.Email, "emailVisibility": context.Current.EmailVisibility, "password": "", "verified": context.Current.Verified, "active": context.Current.Active}[name]
			values[key] = query.ScalarValue{Bool: present && (modifier == "isset" || !authRuleValuesEqual(current, requested))}
		}
	}
	if context.Principal.Kind == identity.User || context.Principal.Kind == identity.Service {
		values["request.auth.id"] = query.ScalarValue{String: context.Principal.ID.String()}
	} else {
		values["request.auth.id"] = query.ScalarValue{String: ""}
	}
	for _, field := range fields {
		kind, ok := authRuleScalarKind(field.Type)
		if !ok {
			continue
		}
		kinds[field.Name] = kind
		values[field.Name] = authRuleScalarValue(kind, context.Current.Fields[field.Name])
		requestName := "request.body." + field.Name
		kinds[requestName] = kind
		values[requestName] = authRuleScalarValue(kind, context.Request[field.Name])
		for _, modifier := range []string{"isset", "changed"} {
			name := requestName + ":" + modifier
			kinds[name] = query.ScalarBool
			_, present := context.Request[field.Name]
			changed := present && !authRuleValuesEqual(context.Current.Fields[field.Name], context.Request[field.Name])
			values[name] = query.ScalarValue{Bool: present && (modifier == "isset" || changed)}
		}
	}
	return kinds, values
}

func authRuleScalarKind(kind schema.FieldType) (query.ScalarKind, bool) {
	switch kind {
	case schema.TypeText, schema.TypeEditor, schema.TypeUUID:
		return query.ScalarString, true
	case schema.TypeBoolean:
		return query.ScalarBool, true
	case schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric:
		return query.ScalarNumber, true
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
		return query.ScalarTime, true
	default:
		return 0, false
	}
}

func authRuleScalarValue(kind query.ScalarKind, raw any) query.ScalarValue {
	value := query.ScalarValue{}
	switch kind {
	case query.ScalarString:
		value.String, _ = raw.(string)
	case query.ScalarBool:
		value.Bool, _ = raw.(bool)
	case query.ScalarNumber:
		switch number := raw.(type) {
		case float64:
			value.Number = number
		case json.Number:
			value.Number, _ = number.Float64()
		case int:
			value.Number = float64(number)
		case int64:
			value.Number = float64(number)
		}
	case query.ScalarTime:
		switch temporal := raw.(type) {
		case time.Time:
			value.Time = temporal
		case string:
			value.Time, _ = time.Parse(time.RFC3339Nano, temporal)
		}
	}
	return value
}

func normalizeAuthRule(rule string) string { return strings.ReplaceAll(rule, "@request.", "request.") }
