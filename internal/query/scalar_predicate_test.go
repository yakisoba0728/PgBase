package query

import "testing"

func TestCompileScalarPredicateUsesSharedBoundedGrammar(t *testing.T) {
	fields := map[string]ScalarKind{"name": ScalarString, "system": ScalarBool, "created": ScalarTime, "fieldCount": ScalarNumber}
	predicate, err := CompileScalarPredicate(`(name:lower ~ 'alpha*' && system = true) || fieldCount >= 3`, fields)
	if err != nil {
		t.Fatal(err)
	}
	if !predicate.Evaluate(map[string]ScalarValue{"name": {String: "Alpha records"}, "system": {Bool: true}, "fieldCount": {Number: 1}}) {
		t.Fatal("expected lower/wildcard predicate to match")
	}
	if !predicate.Evaluate(map[string]ScalarValue{"name": {String: "other"}, "system": {Bool: false}, "fieldCount": {Number: 3}}) {
		t.Fatal("expected numeric predicate to match")
	}
	if predicate.Evaluate(map[string]ScalarValue{"name": {String: "other"}, "system": {Bool: true}, "fieldCount": {Number: 1}}) {
		t.Fatal("unexpected predicate match")
	}
	if _, err = CompileScalarPredicate(`created >= '2026-01-01T00:00:00Z'`, fields); err != nil {
		t.Fatal(err)
	}
	if _, err = CompileScalarPredicate(`unknown = 'value'`, fields); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err = CompileScalarPredicate(`name = system`, fields); err == nil {
		t.Fatal("field comparison accepted")
	}
	if _, err = CompileScalarPredicate(`name:upper = 'alpha'`, fields); err == nil {
		t.Fatal("unsupported modifier accepted")
	}
	if _, err = CompileScalarPredicate(`system ~ 'true'`, fields); err == nil {
		t.Fatal("contains on boolean accepted")
	}
	if _, err = CompileScalarPredicate(`created = 'not-a-time'`, fields); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
}
