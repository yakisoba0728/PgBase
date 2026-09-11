package query

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseAccessRuleUsesFilterGrammarAndPreservesSource(t *testing.T) {
	source := "(title:lower = 'hello' && score >= 2) || title:length = alias:length"
	rule, err := ParsePocketBaseAccessRule(source)
	if err != nil || rule.PocketBase == nil || *rule.PocketBase != source {
		t.Fatalf("rule=%#v err=%v", rule, err)
	}
	collection := schema.Collection{ID: uuid.New(), Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}, {Name: "alias", ColumnName: "alias", Type: schema.TypeText}, {Name: "score", ColumnName: "score", Type: schema.TypeInteger}}}
	sql, args, err := accessrule.Compile(rule, collection, "records", identity.Subject{}, 0, false)
	if err != nil || len(args) != 2 || !strings.Contains(sql, `lower("records"."title")`) || !strings.Contains(sql, `char_length("records"."alias")`) {
		t.Fatalf("sql=%s args=%v err=%v", sql, args, err)
	}
}

func TestPocketBaseAccessRuleAuthRequestAndNullableStringSemantics(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Fields: []schema.Field{{Name: "owner", ColumnName: "owner", Type: schema.TypeUUID}, {Name: "title", ColumnName: "title", Type: schema.TypeText}}}
	user := identity.Subject{Kind: identity.User, ID: uuid.New()}
	for _, source := range []string{"owner = @request.auth.id", "title = null", "title = ''", "@request.body.title:isset = true && @request.body.title:changed = false"} {
		rule, err := ParsePocketBaseAccessRule(source)
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		var sql string
		if strings.Contains(source, "@request.body") {
			sql, _, err = accessrule.CompileWithRequest(rule, collection, "records", user, 0, false, accessrule.RequestContext{Present: map[string]bool{"title": true}, Changed: map[string]bool{"title": false}})
		} else {
			sql, _, err = accessrule.Compile(rule, collection, "records", user, 0, false)
		}
		if err != nil {
			t.Fatalf("%q compile: %v", source, err)
		}
		if source == "title = null" && !strings.Contains(sql, "IS NULL") {
			t.Fatalf("null sql=%s", sql)
		}
		if source == "title = ''" && strings.Contains(sql, "IS NULL") {
			t.Fatalf("empty sql=%s", sql)
		}
	}
}

func TestPocketBaseRecordIDComparisonDoesNotBecomeNativeOwnerRule(t *testing.T) {
	rule, err := ParsePocketBaseAccessRule("id = @request.auth.id")
	if err != nil || rule.Op != "eq" || rule.Field != "id" || rule.Auth != "id" {
		t.Fatalf("rule=%#v err=%v", rule, err)
	}
	if accessrule.OwnerRules().PocketBase().View != nil {
		t.Fatal("native ownership metadata was misrendered as a PocketBase record-id rule")
	}
}

func TestPocketBaseAuthFieldLiteralUsesRequestContext(t *testing.T) {
	rule, err := ParsePocketBaseAccessRule("@request.auth.email = 'manager@example.test'")
	if err != nil {
		t.Fatal(err)
	}
	resolver := pbResolverStubForQuery{}
	sql, args, err := accessrule.CompilePocketBase(rule, accessrule.PBCompileContext{
		Resolver: resolver,
		Request:  &accessrule.RequestContext{Auth: map[string]any{"email": "manager@example.test"}},
	})
	if err != nil || len(args) != 2 || !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") {
		t.Fatalf("sql=%s args=%v err=%v", sql, args, err)
	}
}

func TestPocketBaseAccessRulePreservesFunctions(t *testing.T) {
	for _, source := range []string{"geoDistance(place.lon, place.lat, 127, 37) < 10", "strftime('%Y-%m', created_at, 'start of month', '+1 day') = '2026-10'"} {
		rule, err := ParsePocketBaseAccessRule(source)
		if err != nil || rule.Function == nil || rule.Function.Name == "" {
			t.Fatalf("source=%s rule=%#v err=%v", source, rule, err)
		}
	}
}

func TestPocketBaseAccessRulePreservesExecutionTimeMacro(t *testing.T) {
	rule, err := ParsePocketBaseAccessRule("created_at <= @todayEnd")
	if err != nil || rule.Macro != "@todayEnd" {
		t.Fatalf("rule=%#v err=%v", rule, err)
	}
}

type pbResolverStubForQuery struct{}

func (pbResolverStubForQuery) Resolve(string) (accessrule.PBOperand, error) {
	return accessrule.PBOperand{}, accessrule.ErrInvalid
}

func (pbResolverStubForQuery) ResolveCollection(string, string) (accessrule.PBOperand, error) {
	return accessrule.PBOperand{}, accessrule.ErrInvalid
}
