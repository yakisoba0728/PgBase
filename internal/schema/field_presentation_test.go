package schema

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestFieldPresentationValidation(t *testing.T) {
	base := Catalog{Collections: map[string]Collection{}}
	help := strings.Repeat("도", 300)
	plan, err := CompilePlan(base, NewCreateChange(CreateCollectionInput{Name: "articles", Fields: []FieldInput{{Name: "title", Type: TypeText, Presentable: true, Help: help}, {Name: "secret", Type: TypeText, Hidden: true}}}))
	if err != nil {
		t.Fatalf("CompilePlan(valid): %v", err)
	}
	if !plan.Definition.Fields[0].Presentable || plan.Definition.Fields[0].Help != help || !plan.Definition.Fields[1].Hidden {
		t.Fatalf("metadata lost: %#v", plan.Definition.Fields)
	}
	for _, field := range []FieldInput{{Name: "bad", Type: TypeText, Hidden: true, Presentable: true}, {Name: "bad", Type: TypeText, Help: strings.Repeat("x", 301)}} {
		if _, err := CompilePlan(base, NewCreateChange(CreateCollectionInput{Name: "invalid", Fields: []FieldInput{field}})); err == nil {
			t.Fatalf("accepted invalid metadata: %#v", field)
		}
	}
}

func TestDeclarativeViewInheritsFieldPresentationMetadata(t *testing.T) {
	source := Collection{ID: uuid.New(), Name: "source", TableName: "source", Fields: []Field{{ID: uuid.New(), Name: "secret", ColumnName: "secret", Type: TypeText, Hidden: true, Help: "private", Ordinal: 0}}}
	plan, err := CompilePlan(Catalog{Collections: map[string]Collection{"source": source}}, NewCreateChange(CreateCollectionInput{Name: "projected", View: &ViewInput{Source: "source", Projection: []string{"secret"}}}))
	if err != nil {
		t.Fatal(err)
	}
	field := plan.Definition.Fields[0]
	if !field.Hidden || field.Help != "private" {
		t.Fatalf("view field=%#v", field)
	}
}
