package recordops

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestResolveInputModifiersPocketBaseOrderAndTypes(t *testing.T) {
	max := 4
	collection := schema.Collection{Fields: []schema.Field{
		{Name: "count", Type: schema.TypeInteger},
		{Name: "amount", Type: schema.TypeNumeric},
		{Name: "roles", Type: schema.TypeJSONB, Options: schema.FieldOptions{AllowedValues: []string{"a", "b", "c", "d"}, MaxSelect: &max}},
		{Name: "single", Type: schema.TypeText, Options: schema.FieldOptions{AllowedValues: []string{"a", "b", "c", "d"}}},
	}}
	resolved, err := resolveInputModifiers(collection, map[string]json.RawMessage{
		"count":   json.RawMessage(`10`),
		"count+":  json.RawMessage(`5`),
		"count-":  json.RawMessage(`2`),
		"amount+": json.RawMessage(`"0.25"`),
		"+roles":  json.RawMessage(`["a","b"]`),
		"roles+":  json.RawMessage(`["c","a"]`),
		"roles-":  json.RawMessage(`"b"`),
		"+single": json.RawMessage(`"a"`),
	}, map[string]any{"count": int32(3), "amount": "1.50", "roles": json.RawMessage(`["b","d"]`), "single": "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"count": "13", "amount": `"1.75"`, "roles": `["a","d","c"]`, "single": `"d"`}
	for field, encoded := range want {
		if string(resolved[field]) != encoded {
			t.Errorf("%s = %s, want %s", field, resolved[field], encoded)
		}
	}
	if !reflect.DeepEqual(sortedInputFields(resolved), []string{"amount", "count", "roles", "single"}) {
		t.Fatalf("resolved keys = %v", sortedInputFields(resolved))
	}
}

func TestResolveInputModifiersRejectsUnsupportedAndUnknown(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{{Name: "title", Type: schema.TypeText}, {Name: "count", Type: schema.TypeInteger}}}
	for _, input := range []map[string]json.RawMessage{
		{"title+": json.RawMessage(`"x"`)},
		{"+count": json.RawMessage(`1`)},
		{"missing+": json.RawMessage(`1`)},
	} {
		if _, err := resolveInputModifiers(collection, input, map[string]any{}); err == nil {
			t.Fatalf("accepted modifiers %#v", input)
		}
	}
}

func TestValidateMixedDirectModifierChecksResolvedValue(t *testing.T) {
	maximum := "60"
	collection := schema.Collection{TableName: "items", Fields: []schema.Field{{Name: "count", ColumnName: "count", Type: schema.TypeInteger, Options: schema.FieldOptions{Max: &maximum}}}}
	if err := validateModifierInput(collection, map[string]json.RawMessage{"count": json.RawMessage(`100`), "count-": json.RawMessage(`50`)}); err != nil {
		t.Fatalf("valid resolved value rejected: %v", err)
	}
	if err := validateModifierInput(collection, map[string]json.RawMessage{"count": json.RawMessage(`100`), "count-": json.RawMessage(`20`)}); err == nil {
		t.Fatal("invalid resolved value accepted")
	}
}
