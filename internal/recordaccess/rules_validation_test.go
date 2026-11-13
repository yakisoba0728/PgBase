package recordaccess_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestValidateRulesUsesTheOrdinarySetContract(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "notes", TableName: "notes", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}
	valid := accessrule.OwnerRules()
	valid.List = accessrule.Rule{Op: "eq", Field: "title", Value: json.RawMessage(`"published"`)}
	if err := recordaccess.ValidateRules(catalog, collection, valid); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	flags := schema.Collection{ID: uuid.New(), Name: "flags", TableName: "flags", Fields: []schema.Field{{Name: "name", ColumnName: "name", Type: schema.TypeText}}}
	catalog.Collections[flags.Name] = flags
	pocketBaseSource := `@collection.flags.name ?= "enabled"`
	pocketBase := accessrule.OwnerRules()
	pocketBase.List = accessrule.Rule{Op: "any_eq", Field: "@collection.flags.name", Value: json.RawMessage(`"enabled"`), Binding: accessrule.BindingCatalog, Expression: &pocketBaseSource}
	if err := recordaccess.ValidateRules(catalog, collection, pocketBase); err != nil {
		t.Fatalf("cross-collection rule against final catalog rejected: %v", err)
	}
	delete(catalog.Collections, flags.Name)
	if err := recordaccess.ValidateRules(catalog, collection, pocketBase); err == nil {
		t.Fatal("cross-collection rule accepted without its target catalog entry")
	}
	for _, test := range []struct {
		name string
		rule accessrule.Rule
	}{
		{name: "unsupported operator", rule: accessrule.Rule{Op: "unsupported"}},
		{name: "unknown field", rule: accessrule.Rule{Op: "eq", Field: "missing", Value: json.RawMessage(`"value"`)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := accessrule.OwnerRules()
			candidate.List = test.rule
			err := recordaccess.ValidateRules(catalog, collection, candidate)
			var validation *schema.ValidationError
			if !errors.Is(err, accessrule.ErrInvalid) || !errors.As(err, &validation) || fmt.Sprint(validation.Details) != "[{rules.list invalid_rule}]" {
				t.Fatalf("invalid rules error = %v", err)
			}
		})
	}
}

func TestPocketBaseRulesAcceptSystemFields(t *testing.T) {
	authors := schema.Collection{ID: uuid.New(), Name: "authors", TableName: "authors", Fields: []schema.Field{{Name: "name", ColumnName: "name", Type: schema.TypeText}}}
	notes := schema.Collection{ID: uuid.New(), Name: "notes", TableName: "notes", Fields: []schema.Field{
		{Name: "title", ColumnName: "title", Type: schema.TypeText},
		{ID: uuid.New(), Name: "author", ColumnName: "author", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: authors.ID, Cardinality: schema.RelationSingle}},
	}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{authors.Name: authors, notes.Name: notes}}
	for _, expression := range []string{
		"id != '00000000-0000-0000-0000-000000000000'",
		"id = @request.auth.id",
		"created_at > '2020-01-01T00:00:00Z'",
		"updated_at >= created_at",
		"author.created_at > '2020-01-01T00:00:00Z'",
		"author.id = @request.auth.id",
		"@collection.authors.updated_at > '2020-01-01T00:00:00Z'",
	} {
		rule, err := query.ParseAccessRule(expression)
		if err != nil {
			t.Fatalf("parse %q: %v", expression, err)
		}
		rules := accessrule.OwnerRules()
		rules.List, rules.Update = rule, rule
		if err := recordaccess.ValidateRules(catalog, notes, rules); err != nil {
			t.Fatalf("%q rejected: %v", expression, err)
		}
	}
}
