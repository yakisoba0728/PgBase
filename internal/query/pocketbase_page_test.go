package query

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseFilterJSONStringsRemainSingleLiterals(t *testing.T) {
	for _, test := range []struct{ input, text string }{
		{`title = "from sdk"`, "from sdk"},
		{`title = "from sdk\" || title != \"from sdk"`, `from sdk" || title != "from sdk`},
		{`title = "it's valid"`, "it's valid"},
	} {
		node, err := parsePocketBaseFilterRequest(test.input)
		comparison, ok := node.(filterComparison)
		if err != nil || !ok || comparison.value.text != test.text {
			t.Fatalf("parse %q = %#v, %v", test.input, node, err)
		}
	}
}

func TestPocketBaseSpecialSortSQL(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "records", TableName: "records"}
	for _, test := range []struct{ sort, contains string }{
		{"@random", `random() ASC`},
		{"@rowid", `"id" ASC`},
		{"-@rowid", `"id" DESC`},
	} {
		keys, err := parseSort(test.sort)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := compileRelationListPlan(schema.Catalog{}, collection, nil, effectiveSortKeys(keys))
		if err != nil {
			t.Fatal(err)
		}
		statement, err := plannedRootPlan(nil, plan).pageStatement(nil, 3, 6)
		if err != nil || !strings.Contains(statement.SQL, test.contains) || !strings.Contains(statement.SQL, " OFFSET ") {
			t.Fatalf("sort %s SQL=%q err=%v", test.sort, statement.SQL, err)
		}
	}
}

func TestCountStatementDropsPageOrderAndLimit(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "records", TableName: "records"}
	plan, err := compileRelationListPlan(schema.Catalog{}, collection, nil, []SortKey{{Field: systemFieldID}})
	if err != nil {
		t.Fatal(err)
	}
	statement, err := plannedRootPlan(nil, plan).countStatement()
	if err != nil || strings.Contains(statement.SQL, " ORDER BY ") || strings.Contains(statement.SQL, " LIMIT ") || !strings.HasPrefix(statement.SQL, `SELECT COUNT(DISTINCT "records"."id")`) {
		t.Fatalf("count SQL=%q args=%v err=%v", statement.SQL, statement.Args, err)
	}
}

func TestPocketBasePageNormalization(t *testing.T) {
	for _, test := range []struct{ page, perPage, wantPage, wantPerPage int }{
		{0, 0, 1, 30}, {-2, -3, 1, 30}, {3, 2000, 3, 1000}, {2, 25, 2, 25},
	} {
		page, perPage := normalizePocketBasePage(test.page, test.perPage)
		if page != test.wantPage || perPage != test.wantPerPage {
			t.Fatalf("normalize(%d,%d)=(%d,%d)", test.page, test.perPage, page, perPage)
		}
	}
}
