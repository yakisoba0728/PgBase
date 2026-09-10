package query

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestLowerModifierCompilesAtRootAndRelationTerminal(t *testing.T) {
	root := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", Fields: []schema.Field{
		{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText},
		{ID: uuid.New(), Name: "score", ColumnName: "score", Type: schema.TypeInteger},
	}}
	parsed, err := parseFilter("title:lower = 'hello'")
	if err != nil {
		t.Fatal(err)
	}
	statement, err := CompileList(root, ListQuery{Filter: parsed})
	if err != nil || !strings.Contains(statement.SQL, `lower("title")`) {
		t.Fatalf("root SQL=%q err=%v", statement.SQL, err)
	}
	catalog, relationRoot := relationFilterSortCatalog()
	parsed, err = parseFilter("owner.name:lower = 'ada'")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(catalog, relationRoot, parsed, []SortKey{{Field: "id"}})
	if err != nil {
		t.Fatal(err)
	}
	statement, err = plan.pageStatementWithPolicy(nil, 10, nil, ruleBinding{})
	if err != nil || !strings.Contains(statement.SQL, "lower(") || len(statement.Args) != 2 {
		t.Fatalf("relation SQL=%q args=%v err=%v", statement.SQL, statement.Args, err)
	}
	for _, input := range []string{"score:lower = '1'", "title:upper = 'x'", "title:lower:lower = 'x'"} {
		parsed, err := parseFilter(input)
		if err == nil {
			_, err = compileRelationListPlan(catalog, root, parsed, []SortKey{{Field: "id"}})
		}
		if err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestContainsCompilesAtRootAndRelationTerminal(t *testing.T) {
	root := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", Fields: []schema.Field{
		{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText},
	}}
	parsed, err := parseFilter("title:lower ~ 'hello'")
	if err != nil {
		t.Fatal(err)
	}
	statement, err := CompileList(root, ListQuery{Filter: parsed})
	if err != nil || !strings.Contains(statement.SQL, `strpos(lower("title"), $1) > 0`) {
		t.Fatalf("root SQL=%q args=%v err=%v", statement.SQL, statement.Args, err)
	}
	catalog, relationRoot := relationFilterSortCatalog()
	parsed, err = parseFilter("owner.name ~ 'da'")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(catalog, relationRoot, parsed, []SortKey{{Field: "id"}})
	if err != nil {
		t.Fatal(err)
	}
	statement, err = plan.pageStatementWithPolicy(nil, 10, nil, ruleBinding{})
	if err != nil || !strings.Contains(statement.SQL, "strpos(") || len(statement.Args) != 2 {
		t.Fatalf("relation SQL=%q args=%v err=%v", statement.SQL, statement.Args, err)
	}
}

func TestNotContainsSymmetricContainsAndLengthCompile(t *testing.T) {
	root := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", Fields: []schema.Field{
		{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText},
	}}
	for _, test := range []struct{ filter, sql string }{
		{"title !~ 'draft'", `NOT (strpos("title", $1) > 0)`},
		{"'alphabet' ~ title", `strpos($1, "title") > 0`},
		{"title:length >= 5", `char_length("title") >= $1`},
	} {
		parsed, err := parseFilter(test.filter)
		if err != nil {
			t.Fatalf("parse %q: %v", test.filter, err)
		}
		statement, err := CompileList(root, ListQuery{Filter: parsed})
		if err != nil || !strings.Contains(statement.SQL, test.sql) {
			t.Fatalf("filter=%q SQL=%q err=%v", test.filter, statement.SQL, err)
		}
	}
}

func TestLowerModifierBindsCursorFingerprint(t *testing.T) {
	field := schema.Field{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText}
	plain := &plannedRelationFilter{comparison: &plannedRelationComparison{rootField: &field, input: filterComparison{field: "title", operator: "=", value: filterLiteral{kind: filterLiteralString, text: "hello"}}}}
	lower := &plannedRelationFilter{comparison: &plannedRelationComparison{rootField: &field, input: filterComparison{field: "title", modifier: "lower", operator: "=", value: filterLiteral{kind: filterLiteralString, text: "hello"}}}}
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	plainHash, err := cursorV5QueryFingerprint(codec, plain)
	if err != nil {
		t.Fatal(err)
	}
	lowerHash, err := cursorV5QueryFingerprint(codec, lower)
	if err != nil {
		t.Fatal(err)
	}
	if string(plainHash) == string(lowerHash) {
		t.Fatal("lower modifier was omitted from cursor query identity")
	}
}
