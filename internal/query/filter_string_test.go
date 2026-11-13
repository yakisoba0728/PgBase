package query

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestNativeFilterJSONStringsRemainBoundParameters(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "records", TableName: "records", Fields: []schema.Field{
		{Name: "title", ColumnName: "title", Type: schema.TypeText},
	}}
	order, err := scalarOrderSpec(collection, effectiveSortKeys(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"", "it's valid", "한글 日本語", "quote \" and slash \\", "line\nreturn\rtab\t", `x" || title != "x`, `@request.auth.id = 'admin'`,
	} {
		literal, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := parseFilterRequest("title = " + string(literal))
		if err != nil {
			t.Fatalf("parse %s: %v", literal, err)
		}
		comparison, ok := filter.(filterComparison)
		if !ok || comparison.value.text != value {
			t.Fatalf("literal changed syntax: %#v", filter)
		}
		statement, err := compileListPageOffset(filter, order, nil, 2, 0, nil)
		if err != nil || len(statement.Args) != 2 || statement.Args[0] != value || strings.Contains(statement.SQL, " || ") {
			t.Fatalf("literal escaped parameter binding: %#v, %v", statement, err)
		}
	}
	for _, expression := range []string{`title = "\uD55C\uAE00"`, `title = 'it''s valid'`, `title = 'say "hello"'`} {
		if _, err := parseFilterRequest(expression); err != nil {
			t.Fatalf("rejected supported quoted string %q: %v", expression, err)
		}
	}
}

func TestNativeFilterRejectsMalformedJSONStrings(t *testing.T) {
	for _, literal := range []string{`"unterminated`, `"trailing\`, `"bad\x20escape"`, `"bad\qescape"`, "\"raw\nnewline\"", `"quote" ||`, `"valid" "extra"`} {
		if _, err := parseFilterRequest("title = " + literal); err == nil {
			t.Fatalf("accepted malformed JSON literal %q", literal)
		}
	}
}
