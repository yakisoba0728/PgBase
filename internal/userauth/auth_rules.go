package userauth

import (
	"strings"

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

func ValidateAuthRecordRules(rules AuthCollectionRules, fields []schema.FieldInput) bool {
	for _, rule := range []string{rules.List, rules.View, rules.Create, rules.Update, rules.Delete, rules.Auth, rules.Manage} {
		if len(rule) > 2048 {
			return false
		}
		trimmed := strings.TrimSpace(rule)
		if trimmed == "" || trimmed == "true" || trimmed == "false" {
			continue
		}
		parsed, err := query.ParseAccessRule(rule)
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
