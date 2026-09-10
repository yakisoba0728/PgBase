package accessrule

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type pbResolverStub map[string]PBOperand

func (r pbResolverStub) Resolve(path string) (PBOperand, error) {
	value, ok := r[path]
	if !ok {
		return PBOperand{}, errors.New("unknown")
	}
	return value, nil
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
