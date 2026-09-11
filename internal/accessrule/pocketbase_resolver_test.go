package accessrule

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

type pbResolverStub map[string]PBOperand

func (r pbResolverStub) Resolve(path string) (PBOperand, error) {
	value, ok := r[path]
	if !ok {
		return PBOperand{}, errors.New("unknown")
	}
	return value, nil
}

func TestCompilePocketBaseAuthenticationFailsClosedForInvalidSubjects(t *testing.T) {
	resolver := pbResolverStub{}
	validID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")
	tests := []struct {
		name      string
		principal identity.Subject
		want      bool
	}{
		{name: "anonymous", principal: identity.Subject{Kind: identity.Anonymous}},
		{name: "user", principal: identity.Subject{Kind: identity.User, ID: validID}, want: true},
		{name: "administrator", principal: identity.Subject{Kind: identity.Administrator, ID: validID}, want: true},
		{name: "service", principal: identity.Subject{Kind: identity.Service, ID: validID}, want: true},
		{name: "zero subject"},
		{name: "unknown kind", principal: identity.Subject{Kind: "future", ID: validID}},
		{name: "user without ID", principal: identity.Subject{Kind: identity.User}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, args, err := CompilePocketBase(Rule{Op: "authenticated"}, PBCompileContext{Resolver: resolver, Principal: test.principal})
			if err != nil || len(args) != 1 || args[0] != test.want {
				t.Fatalf("authenticated args=%v err=%v, want %t", args, err, test.want)
			}
			_, args, err = CompilePocketBase(Rule{Op: "unauthenticated"}, PBCompileContext{Resolver: resolver, Principal: test.principal})
			if err != nil || len(args) != 1 || args[0] != !test.want {
				t.Fatalf("unauthenticated args=%v err=%v, want %t", args, err, !test.want)
			}
		})
	}
}
func (r pbResolverStub) ResolveCollection(collection, path string) (PBOperand, error) {
	return r.Resolve("@collection." + collection + "." + path)
}

func TestCompilePocketBaseUsesResolverCardinalityAndOffset(t *testing.T) {
	resolver := pbResolverStub{
		"title":     {SQL: `r."title"`, Kind: PBText, Cardinality: PBScalar},
		"tags.name": {Kind: PBText, Cardinality: PBMany, Multi: &PBMulti{From: `"app"."tags" t`, Predicate: `t."owner"=r."id"`, ValueSQL: `t."name"`}},
	}
	rule := Rule{Op: "all", Args: []Rule{{Op: "contains", Field: "title", Value: json.RawMessage(`"needle"`)}, {Op: "any_eq", Field: "tags.name", Value: json.RawMessage(`"blue"`)}}}
	sql, args, err := CompilePocketBase(rule, PBCompileContext{Resolver: resolver, ArgOffset: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "EXISTS") || !strings.Contains(sql, "$4") || !strings.Contains(sql, "$5") || len(args) != 2 {
		t.Fatalf("sql=%s args=%v", sql, args)
	}
}

func TestCompilePocketBaseResolvesRequestAndCollectionOperands(t *testing.T) {
	resolver := pbResolverStub{"title": {SQL: `r."title"`, Kind: PBText, Cardinality: PBScalar}, "@collection.flags.name": {Kind: PBText, Cardinality: PBMany, Multi: &PBMulti{From: `"app"."flags" f`, ValueSQL: `f."name"`}}}
	rule := Rule{Op: "all", Args: []Rule{{Op: "eq", Field: "__pgbase_request_body_title", Value: json.RawMessage(`"new"`)}, {Op: "any_eq", Field: "@collection.flags.name", Value: json.RawMessage(`"enabled"`)}}}
	sql, args, err := CompilePocketBase(rule, PBCompileContext{Resolver: resolver, Request: &RequestContext{Body: map[string]json.RawMessage{"title": json.RawMessage(`"new"`)}}, ArgOffset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "EXISTS") || len(args) != 3 {
		t.Fatalf("sql=%s args=%v", sql, args)
	}
}

func TestCompilePocketBaseCastsRequestBooleanParameters(t *testing.T) {
	rule := Rule{Op: "eq", Field: "__pgbase_request_body_title:isset", Value: json.RawMessage(`true`)}
	resolver := pbResolverStub{"title": {SQL: `r."title"`, Kind: PBText, Cardinality: PBScalar}}
	sql, args, err := CompilePocketBase(rule, PBCompileContext{Resolver: resolver, Request: &RequestContext{Present: map[string]bool{"title": true}}})
	if err != nil || len(args) != 2 || !strings.Contains(sql, "$1::boolean") || !strings.Contains(sql, "$2::boolean") {
		t.Fatalf("sql=%s args=%v err=%v", sql, args, err)
	}
}

func TestCompilePocketBaseFunctionsAndModifiers(t *testing.T) {
	resolver := pbResolverStub{
		"place.lon":  {SQL: `r."lon"`, Kind: PBNumber, Cardinality: PBScalar},
		"place.lat":  {SQL: `r."lat"`, Kind: PBNumber, Cardinality: PBScalar},
		"created_at": {SQL: `r."created_at"`, Kind: PBTime, Cardinality: PBScalar},
	}
	geo := Rule{Op: "lt", Field: "__pgbase_function", Function: &PBFunction{Name: "geoDistance", Args: []PBFuncArg{{Field: "place.lon"}, {Field: "place.lat"}, {Value: json.RawMessage(`127`)}, {Value: json.RawMessage(`37`)}}}, Value: json.RawMessage(`10`)}
	if sql, _, err := CompilePocketBase(geo, PBCompileContext{Resolver: resolver}); err != nil || !strings.Contains(sql, "acos") {
		t.Fatalf("sql=%s err=%v", sql, err)
	}
	formatted := Rule{Op: "eq", Field: "__pgbase_function", Function: &PBFunction{Name: "strftime", Args: []PBFuncArg{{Value: json.RawMessage(`"%Y-%m"`)}, {Field: "created_at"}, {Value: json.RawMessage(`"start of month"`)}, {Value: json.RawMessage(`"+1 day"`)}}}, Value: json.RawMessage(`"2026-10"`)}
	if sql, _, err := CompilePocketBase(formatted, PBCompileContext{Resolver: resolver}); err != nil || !strings.Contains(sql, "date_trunc") || !strings.Contains(sql, "::interval") {
		t.Fatalf("sql=%s err=%v", sql, err)
	}
}

func TestCompilePocketBaseUsesExecutionTimeUTCMetricMacro(t *testing.T) {
	rule := Rule{Op: "lte", Field: "created_at", Macro: "@todayEnd", Value: json.RawMessage(`"ignored parse snapshot"`)}
	resolver := pbResolverStub{"created_at": {SQL: `r."created_at"`, Kind: PBTime, Cardinality: PBScalar}}
	sql, args, err := CompilePocketBase(rule, PBCompileContext{Resolver: resolver})
	if err != nil || len(args) != 0 || !strings.Contains(sql, "CURRENT_TIMESTAMP") || !strings.Contains(sql, "1 millisecond") {
		t.Fatalf("sql=%s args=%v err=%v", sql, args, err)
	}
}
