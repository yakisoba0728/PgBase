package recordaccess_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
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
	pocketBase.List = accessrule.Rule{Op: "any_eq", Field: "@collection.flags.name", Value: json.RawMessage(`"enabled"`), PocketBase: &pocketBaseSource}
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
			if err := recordaccess.ValidateRules(catalog, collection, candidate); err == nil {
				t.Fatal("invalid rules accepted")
			}
		})
	}
}
