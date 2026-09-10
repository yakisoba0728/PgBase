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

func TestAdvancedIndexPlannerRejectsCreateOptionsTheParserWouldDiscard(t *testing.T) {
	catalog := planTestCatalog(4, updateTestCollection())
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"concurrently", "CREATE INDEX CONCURRENTLY lossy ON posts (title)"},
		{"or replace", "CREATE OR REPLACE INDEX lossy ON posts (title)"},
		{"temporary", "CREATE TEMPORARY INDEX lossy ON posts (title)"},
		{"temp unique", "CREATE TEMP UNIQUE INDEX lossy ON posts (title)"},
		{"include", "CREATE INDEX lossy ON posts (title) INCLUDE (views)"},
		{"storage parameters", "CREATE INDEX lossy ON posts (title) WITH (fillfactor=70)"},
		{"tablespace", "CREATE INDEX lossy ON posts (title) TABLESPACE pg_default"},
		{"only", "CREATE INDEX lossy ON ONLY posts (title)"},
		{"operator class", "CREATE INDEX lossy ON posts (title text_pattern_ops)"},
		{"null ordering", "CREATE INDEX lossy ON posts (title NULLS LAST)"},
		{"nulls not distinct", "CREATE UNIQUE INDEX lossy ON posts (title) NULLS NOT DISTINCT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{
				Name: "posts",
				Fields: []FieldInput{
					{Name: "title", Type: TypeText, Required: true, Indexed: true},
					{Name: "views", Type: TypeBigInt},
				},
				Indexes: &[]IndexInput{{Name: "lossy", RawSQL: test.raw}},
			}))
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || len(validationErr.Details) != 1 ||
				validationErr.Details[0].Field != "indexes.0.rawSql" || validationErr.Details[0].Code != "invalid_index_sql" {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestAdvancedIndexPlannerTreatsConcurrentTextBySQLTokenContext(t *testing.T) {
	catalog := planTestCatalog(4, updateTestCollection())
	fields := []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "views", Type: TypeBigInt},
		{Name: "concurrently", Type: TypeText},
	}
	for _, test := range []struct {
		name      string
		indexName string
		raw       string
	}{
		{"comment", "comment_safe", "CREATE /* CONCURRENTLY */ INDEX comment_safe ON posts (title)"},
		{"quoted identifier", "quoted_safe", `CREATE INDEX quoted_safe ON posts ("concurrently")`},
		{"literal", "literal_safe", "CREATE INDEX literal_safe ON posts ((lower(title) || 'concurrently'))"},
		{"quoted index name", "concurrently", `CREATE INDEX "concurrently" ON posts (title)`},
		{"quoted contextual index name", "if", `CREATE INDEX "if" ON posts (title)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{
				Name: "posts", Fields: fields,
				Indexes: &[]IndexInput{{Name: test.indexName, RawSQL: test.raw}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Definition.Indexes) != 1 {
				t.Fatalf("indexes = %#v", plan.Definition.Indexes)
			}
			// The durable canonical form must itself remain valid input.
			replayed := plan.Definition.Indexes[0]
			if _, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{
				Name: "posts", Fields: fields, Indexes: &[]IndexInput{{Name: replayed.Name, RawSQL: replayed.RawSQL}},
			})); err != nil {
				t.Fatalf("canonical raw SQL cannot be replayed: raw=%q err=%v", replayed.RawSQL, err)
			}
		})
	}
}
