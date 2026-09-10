package doctor

import (
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

// TestFunctionDigestRejectsACommentedOutBody fixes the property that replaced
// substring matching: a body that still contains every fragment a reader might
// look for, with the executable part removed, is a different function.
func TestFunctionDigestRejectsACommentedOutBody(t *testing.T) {
	planner := schema.FunctionSpec{
		Namespace:      "pgbase_relation",
		Name:           "position_check",
		ReturnsTrigger: true,
		Language:       "plpgsql",
		Volatility:     "v",
		Kind:           "f",
		Parallel:       "u",
		BodySQL: `
BEGIN
	IF TG_OP = 'INSERT' THEN
		affected_sources := ARRAY[NEW.source_id];
	END IF;
	RAISE EXCEPTION 'position gap' USING ERRCODE = '23514';
	RETURN NULL;
END;
`,
	}
	spoofed := planner
	spoofed.BodySQL = `
BEGIN
	-- IF TG_OP = 'INSERT' THEN
	--   affected_sources := ARRAY[NEW.source_id];
	-- END IF;
	-- RAISE EXCEPTION 'position gap' USING ERRCODE = '23514';
	RETURN NULL;
END;
`
	if functionDefinitionDigest(planner) == functionDefinitionDigest(spoofed) {
		t.Fatal("a commented-out body produced the planner's function digest")
	}
	if !strings.Contains(spoofed.BodySQL, "23514") {
		t.Fatal("the spoofed body must keep the fragments a substring matcher looked for")
	}
	if functionDefinitionDigest(planner) != functionDefinitionDigest(planner) {
		t.Fatal("the function digest is not stable")
	}
}

// TestFunctionDigestSeparatesAdjacentFields proves the length-delimited
// encoding: no value can impersonate a field boundary by containing whatever
// separator the digest uses.
func TestFunctionDigestSeparatesAdjacentFields(t *testing.T) {
	left := schema.FunctionSpec{Namespace: "pgbase_relation", Name: "ab", BodySQL: "c"}
	right := schema.FunctionSpec{Namespace: "pgbase_relation", Name: "a", BodySQL: "bc"}
	if functionDefinitionDigest(left) == functionDefinitionDigest(right) {
		t.Fatal("two different function definitions share a digest")
	}
}

func TestPhysicalDefaultsEquivalentAcceptsEveryRenderingOfOneValue(t *testing.T) {
	tests := []struct {
		name              string
		dataType          string
		planner, observed string
		want              bool
	}{
		{name: "timestamptz cast spelling", dataType: "timestamp with time zone",
			planner:  `E'2026-08-28T12:34:56.123456Z'::timestamptz`,
			observed: `'2026-08-28 12:34:56.123456+00'::timestamp with time zone`, want: true},
		{name: "timestamptz value drift", dataType: "timestamp with time zone",
			planner:  `E'2026-08-28T12:34:56.123456Z'::timestamptz`,
			observed: `'2026-08-28 12:34:57.123456+00'::timestamp with time zone`, want: false},
		{name: "numeric cast", dataType: "numeric", planner: `3.40::numeric`, observed: `3.4`, want: true},
		{name: "numeric drift", dataType: "numeric", planner: `3.40::numeric`, observed: `3.5`, want: false},
		{name: "boolean case", dataType: "boolean", planner: `TRUE`, observed: `true`, want: true},
		{name: "text escape form", dataType: "text", planner: `E'a''b'::text`, observed: `'a''b'::text`, want: true},
		{name: "jsonb key order", dataType: "jsonb", planner: `E'{"a":1,"z":2}'::jsonb`, observed: `'{"z": 2, "a": 1}'::jsonb`, want: true},
		{name: "now", dataType: "timestamp with time zone", planner: `CURRENT_TIMESTAMP`, observed: `now()`, want: false},
		{name: "both absent", dataType: "text", planner: ``, observed: ``, want: true},
		{name: "only observed", dataType: "text", planner: ``, observed: `'x'::text`, want: false},
		{name: "unparsable falls back to text", dataType: "text",
			planner: `substr('abc', 1)`, observed: `substr( 'abc' , 1 )`, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := physicalDefaultsEquivalent(test.dataType, test.planner, test.observed); got != test.want {
				t.Fatalf("physicalDefaultsEquivalent(%q, %q, %q) = %t, want %t",
					test.dataType, test.planner, test.observed, got, test.want)
			}
		})
	}
}
