package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestCompoundIndexValidationRejectsUnsafeShapes(t *testing.T) {
	base := []FieldInput{{Name: "a", Type: TypeText}, {Name: "b", Type: TypeBigInt}, {Name: "blob", Type: TypeFile}}
	for _, test := range []struct {
		name  string
		index IndexInput
	}{
		{"one field", IndexInput{Name: "lookup", Fields: []string{"a"}}},
		{"duplicate field", IndexInput{Name: "lookup", Fields: []string{"a", "a"}}},
		{"missing field", IndexInput{Name: "lookup", Fields: []string{"a", "missing"}}},
		{"virtual field", IndexInput{Name: "lookup", Fields: []string{"a", "blob"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(CreateCollectionInput{Name: "items", Fields: base, Indexes: []IndexInput{test.index}}))
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("CompilePlan error=%v", err)
			}
		})
	}
}

func TestCompoundIndexOrderAndUniquenessChangesRebuild(t *testing.T) {
	current := Collection{Name: "items", TableName: "items", Fields: []Field{{Name: "a", ColumnName: "a", Type: TypeText}, {Name: "b", ColumnName: "b", Type: TypeBigInt}}, Indexes: []Index{{Name: "lookup", IndexName: compoundIndexName("items", "lookup"), Fields: []string{"a", "b"}}}}
	indexes := []IndexInput{{Name: "lookup", Fields: []string{"b", "a"}, Unique: true}}
	plan, err := CompilePlan(Catalog{Collections: map[string]Collection{"items": current}}, NewUpdateChange(UpdateCollectionInput{Name: "items", Fields: []FieldInput{{Name: "a", Type: TypeText}, {Name: "b", Type: TypeBigInt}}, Indexes: &indexes}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 2 || !hasSQLPrefix(plan.Statements, "DROP INDEX") || !hasSQLPrefix(plan.Statements, "CREATE UNIQUE INDEX") {
		t.Fatalf("statements=%v", plan.Statements)
	}
}

func hasSQLPrefix(items []string, prefix string) bool {
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}
