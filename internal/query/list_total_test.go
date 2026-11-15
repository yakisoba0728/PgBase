package query

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestListExactTotalOnlyWhenThePageProvesIt(t *testing.T) {
	items := func(count int) []map[string]any { return make([]map[string]any, count) }
	for _, test := range []struct {
		name    string
		result  listResult
		request ListRequest
		total   int64
		exact   bool
	}{
		{"sparse first page", listResult{ordered: true, page: RecordPage{Items: items(3)}}, ListRequest{}, 3, true},
		{"empty first page", listResult{ordered: true, page: RecordPage{Items: items(0)}}, ListRequest{}, 0, true},
		// A page of exactly the limit with no next cursor was fetched with a
		// lookahead row, so it is still the whole result.
		{"exactly page size, no cursor", listResult{ordered: true, page: RecordPage{Items: items(200)}}, ListRequest{Limit: 200}, 200, true},
		{"exactly page size, next cursor", listResult{ordered: true, page: RecordPage{Items: items(200), NextCursor: "next"}}, ListRequest{Limit: 200}, 0, false},
		{"byte-trimmed page", listResult{ordered: true, page: RecordPage{Items: items(5), NextCursor: "next"}}, ListRequest{Limit: 200}, 0, false},
		{"continuation page", listResult{ordered: true, page: RecordPage{Items: items(3)}}, ListRequest{Cursor: "c"}, 0, false},
		{"random sort", listResult{page: RecordPage{Items: items(3)}}, ListRequest{}, 0, false},
		{"offset page with rows", listResult{ordered: true, page: RecordPage{Items: items(2)}}, ListRequest{Offset: 4}, 6, true},
		{"offset past the end", listResult{ordered: true, page: RecordPage{Items: items(0)}}, ListRequest{Offset: 4}, 0, false},
	} {
		total, exact := test.result.exactTotal(test.request)
		if total != test.total || exact != test.exact {
			t.Errorf("%s: exactTotal = %d/%v, want %d/%v", test.name, total, exact, test.total, test.exact)
		}
	}
}

func TestContainsCompilesToOneEscapedLikePattern(t *testing.T) {
	root := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", Fields: []schema.Field{
		{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText},
	}}
	for _, test := range []struct{ filter, sql, pattern string }{
		{`title ~ '50%_off\x'`, `("title" LIKE $1 ESCAPE '\')`, `%50\%\_off\\x%`},
		{`title !~ 'Draft'`, `("title" NOT LIKE $1 ESCAPE '\')`, `%Draft%`},
		// :lower lowers only the column, exactly as the strpos form did.
		{`title:lower ~ 'MiXed'`, `(lower("title") LIKE $1 ESCAPE '\')`, `%MiXed%`},
		{`title ~ ''`, `("title" LIKE $1 ESCAPE '\')`, `%%`},
	} {
		parsed, err := parseFilter(test.filter)
		if err != nil {
			t.Fatalf("parse %q: %v", test.filter, err)
		}
		statement, err := CompileList(root, ListQuery{Filter: parsed})
		if err != nil || !strings.Contains(statement.SQL, test.sql) || strings.Contains(statement.SQL, "strpos") {
			t.Fatalf("%s: SQL=%q err=%v", test.filter, statement.SQL, err)
		}
		if !reflect.DeepEqual(statement.Args[0], test.pattern) {
			t.Fatalf("%s: pattern = %#v, want %#v", test.filter, statement.Args[0], test.pattern)
		}
	}
}
