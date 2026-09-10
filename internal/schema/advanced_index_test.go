package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestAdvancedIndexPlannerAcceptsStructuredAndSingleStatementRawSQL(t *testing.T) {
	catalog := planTestCatalog(4, updateTestCollection())
	for _, test := range []struct {
		name  string
		index IndexInput
		want  string
	}{
		{"structured", IndexInput{Name: "title_search", Keys: []IndexKeyInput{{Expression: "lower(title)", Collation: "C", Order: "desc"}}, Where: "title is not null"}, "lower(title)"},
		{"raw", IndexInput{Name: "title_raw", RawSQL: "CREATE INDEX title_raw ON posts ((lower(title))) WHERE title IS NOT NULL"}, "lower(title)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}, {Name: "views", Type: TypeBigInt}}, Indexes: &[]IndexInput{test.index}}))
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Definition.Indexes) != 1 || !strings.Contains(strings.Join(plan.Statements, "\n"), test.want) {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}
}

func TestAdvancedIndexPlannerRejectsSQLEscapeAndInvalidShapes(t *testing.T) {
	catalog := planTestCatalog(4, updateTestCollection())
	values := []IndexInput{
		{Name: "escape", RawSQL: "CREATE INDEX escape ON posts (title); DROP TABLE app.posts"},
		{Name: "wrong_table", RawSQL: "CREATE INDEX wrong_table ON others (title)"},
		{Name: "both", Fields: []string{"title", "views"}, Keys: []IndexKeyInput{{Field: "title"}}},
		{Name: "unsafe", Keys: []IndexKeyInput{{Expression: "pg_sleep(1)"}}},
	}
	for _, index := range values {
		_, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}, {Name: "views", Type: TypeBigInt}}, Indexes: &[]IndexInput{index}}))
		if err == nil || !errors.As(err, new(*ValidationError)) {
			t.Fatalf("index %q error = %v", index.Name, err)
		}
	}
}
