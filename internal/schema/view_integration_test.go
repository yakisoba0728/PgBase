//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDeclarativeViewCreateFilterReadOnlyAndDependencies(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	source := applyFreshPlan(t, service, CreateCollectionInput{Name: "articles", Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true}, {Name: "status", Type: TypeText, Required: true}, {Name: "score", Type: TypeBigInt}}}, administratorID)
	if _, err := pool.Exec(context.Background(), `insert into app.articles(id,created_at,updated_at,title,status,score) values(gen_random_uuid(),now(),now(),'visible','published',5),(gen_random_uuid(),now(),now(),'hidden','draft',10)`); err != nil {
		t.Fatal(err)
	}
	filter := &ViewFilter{Op: "all", Args: []ViewFilter{{Op: "eq", Field: "status", Value: json.RawMessage(`"published"`)}, {Op: "gte", Field: "score", Value: json.RawMessage(`5`)}}}
	if _, err := service.Preview(context.Background(), NewCreateChange(CreateCollectionInput{Name: "published_articles", View: &ViewInput{Source: "articles", Projection: []string{"title", "score"}, Filter: filter}})); err != nil {
		var detail *ValidationError
		if errors.As(err, &detail) {
			t.Fatalf("view preview details=%+v", detail.Details)
		}
		t.Fatal(err)
	}
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "published_articles", View: &ViewInput{Source: "articles", Projection: []string{"title", "score"}, Filter: filter}}, administratorID)
	if view.View == nil || view.View.SourceCollectionID != source.ID || view.AccessCollectionID() != source.ID {
		t.Fatalf("view metadata=%+v", view.View)
	}
	var titles []string
	rows, err := pool.Query(context.Background(), `select title from app.published_articles order by title`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, title)
	}
	rows.Close()
	if len(titles) != 1 || titles[0] != "visible" {
		t.Fatalf("view rows=%v", titles)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stored := catalog.Collections["published_articles"]
	if stored.View == nil || len(stored.Fields) != 2 || stored.Fields[0].Name != "title" {
		t.Fatalf("stored view=%+v", stored)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range manifest.Required().Tables {
		if table.Name == "published_articles" {
			assertPostgresTableSpec(t, pool, table)
		}
	}
	for _, column := range manifest.Required().Columns {
		if column.Table == "published_articles" {
			assertPostgresColumnSpec(t, pool, column)
		}
	}
	if _, err := service.Preview(context.Background(), NewUpdateChange(UpdateCollectionInput{Name: "published_articles", Fields: []FieldInput{{Name: "title", Type: TypeText}}})); err == nil {
		t.Fatal("view update was writable")
	}
	sourceUpdate := UpdateCollectionInput{Name: "articles", Fields: []FieldInput{{Name: "status", Type: TypeText}, {Name: "score", Type: TypeBigInt}}}
	if _, err := service.Preview(context.Background(), NewUpdateChange(sourceUpdate)); err == nil {
		t.Fatal("projected source field drop was allowed")
	}
	if _, err := service.Preview(context.Background(), NewDeleteChange("articles")); err == nil {
		t.Fatal("referenced source deletion was allowed")
	}
	deletePlan, err := service.Preview(context.Background(), NewDeleteChange("published_articles"))
	if err != nil {
		t.Fatal(err)
	}
	if len(deletePlan.Statements) != 1 || deletePlan.Statements[0] != `DROP VIEW "app"."published_articles"` {
		t.Fatalf("delete SQL=%v", deletePlan.Statements)
	}
	var validationError *ValidationError
	_, err = CompilePlan(catalog, NewCreateChange(CreateCollectionInput{Name: "unsafe_view", View: &ViewInput{Source: "articles", Projection: []string{"title"}, Filter: &ViewFilter{Op: "eq", Field: "missing", Value: json.RawMessage(`"x"`)}}}))
	if !errors.As(err, &validationError) {
		t.Fatalf("invalid filter error=%v", err)
	}
}
