package userauth

import (
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthRecordRuleEvaluatesTypedCurrentAuthAndRequestModifiers(t *testing.T) {
	fields := []schema.FieldInput{{Name: "team", Type: schema.TypeText}, {Name: "score", Type: schema.TypeInteger}}
	id := uuid.New()
	context := AuthRuleContext{
		Principal: identity.Subject{Kind: identity.User, ID: id},
		Current:   AuthRecord{ID: id, Verified: true, Active: true, Fields: map[string]any{"team": "alpha", "score": float64(4)}},
		Request:   map[string]any{"team": "beta"},
	}
	rule := `@request.auth.id = id && verified = true && score >= 4 && @request.body.team:isset = true && @request.body.team:changed = true`
	kinds, values := authRuleScalars(fields, context)
	for _, part := range []string{`@request.auth.id = id`, `verified = true`, `score >= 4`, `@request.body.team:isset = true`, `@request.body.team:changed = true`} {
		if _, partErr := query.CompileScalarPredicate(normalizeAuthRule(part), kinds); partErr != nil {
			t.Fatalf("compile auth rule part %q: %#v", part, partErr)
		}
	}
	predicate, err := query.CompileScalarPredicate(normalizeAuthRule(rule), kinds)
	if err != nil {
		t.Fatalf("compile auth rule: %#v", err)
	}
	if !predicate.Evaluate(values) {
		t.Fatalf("compiled values did not match: %#v", values)
	}
	if !EvaluateAuthRecordRule(rule, fields, context) {
		t.Fatal("typed auth rule did not match")
	}
	if EvaluateAuthRecordRule(`@request.body.score:isset = true`, fields, context) {
		t.Fatal("missing request field reported as set")
	}
	if EvaluateAuthRecordRule(rule, fields, AuthRuleContext{Principal: identity.Subject{Kind: identity.Anonymous}, Current: context.Current, Request: context.Request}) {
		t.Fatal("anonymous request matched authenticated self rule")
	}
}

func TestAuthRecordRuleConstantsAndValidation(t *testing.T) {
	if !EvaluateAuthRecordRule("", nil, AuthRuleContext{}) || !EvaluateAuthRecordRule("true", nil, AuthRuleContext{}) || EvaluateAuthRecordRule("false", nil, AuthRuleContext{}) {
		t.Fatal("constant rule semantics changed")
	}
	if ValidateAuthRecordRules(AuthCollectionRules{View: "missing = true"}, nil) {
		t.Fatal("unknown auth rule field accepted")
	}
}

func TestAuthRecordRulesAcceptAndEvaluateEmailVisibility(t *testing.T) {
	rules := AuthCollectionRules{View: "emailVisibility = true"}
	if !ValidateAuthRecordRules(rules, nil) {
		t.Fatal("emailVisibility rule was rejected")
	}
	if !EvaluateAuthRecordRule(rules.View, nil, AuthRuleContext{Current: AuthRecord{EmailVisibility: true}}) {
		t.Fatal("emailVisibility rule did not evaluate")
	}
	if EvaluateAuthRecordRule(rules.View, nil, AuthRuleContext{Current: AuthRecord{EmailVisibility: false}}) {
		t.Fatal("emailVisibility rule accepted false value")
	}
}
