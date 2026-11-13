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

func TestNumericModifiersRejectUnsupportedOperandEncodings(t *testing.T) {
	for _, kind := range []schema.FieldType{schema.TypeNumeric, schema.TypeBigInt} {
		field := schema.Field{Name: "amount", Type: kind}
		for _, value := range []string{"1e-2", "1/2", "0x10"} {
			raw, _ := json.Marshal(value)
			if got, err := modifyNumber(field, "1", raw, modifierAppend); err == nil {
				t.Errorf("%s modifier accepted %q as %#v", kind, value, got)
			}
		}
		if got, err := modifyNumber(field, "1/2", json.RawMessage(`"1"`), modifierAppend); err == nil {
			t.Errorf("%s modifier accepted invalid direct assignment as %#v", kind, got)
		}
	}
	field := schema.Field{Name: "amount", Type: schema.TypeBigInt}
	if got, err := modifyNumber(field, "1.5", json.RawMessage(`"0.5"`), modifierAppend); err == nil {
		t.Errorf("bigint modifier accepted fractional operands as %#v", got)
	}
}

func TestBigIntModifiersPreserveExactIntegers(t *testing.T) {
	field := schema.Field{Name: "amount", Type: schema.TypeBigInt}
	got, err := modifyNumber(field, "9007199254740993", json.RawMessage(`"2"`), modifierAppend)
	if err != nil || got != "9007199254740995" {
		t.Fatalf("bigint increment = %#v, %v", got, err)
	}
}

func TestResolveInputModifiersPreservesExactNumericTransport(t *testing.T) {
	for _, kind := range []schema.FieldType{schema.TypeNumeric, schema.TypeBigInt} {
		collection := schema.Collection{Fields: []schema.Field{{Name: "amount", Type: kind}}}
		input := map[string]json.RawMessage{"amount": json.RawMessage(`9007199254740993`), "amount+": json.RawMessage(`"0"`)}
		if got, err := resolveInputModifiers(collection, input, nil); err == nil {
			t.Errorf("%s modifier accepted unquoted direct number: %#v", kind, got)
		}
		input["amount"] = json.RawMessage(`"9007199254740993"`)
		got, err := resolveInputModifiers(collection, input, nil)
		if err != nil || string(got["amount"]) != `"9007199254740993"` {
			t.Errorf("%s exact direct assignment + modifier = %s, %v", kind, got["amount"], err)
		}
	}
}
