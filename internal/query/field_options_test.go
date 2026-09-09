package query

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestDecodeInputEnforcesTextAndMultiSelectOptions(t *testing.T) {
	min, max := 1, 2
	collection := schema.Collection{Fields: []schema.Field{
		{Name: "email", Type: schema.TypeText, Options: schema.FieldOptions{Format: "email", AllowedDomains: []string{"example.com"}, DeniedDomains: []string{"private.example.com"}}},
		{Name: "labels", Type: schema.TypeJSONB, Options: schema.FieldOptions{AllowedValues: []string{"a", "b"}, MinSelect: &min, MaxSelect: &max}},
	}}
	if _, err := decodeInput(collection, map[string]json.RawMessage{"email": json.RawMessage(`"person@example.com"`), "labels": json.RawMessage(`["a","b"]`)}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []map[string]json.RawMessage{{"email": json.RawMessage(`"person@other.test"`)}, {"email": json.RawMessage(`"person@child.private.example.com"`)}, {"labels": json.RawMessage(`["c"]`)}, {"labels": json.RawMessage(`"a"`)}} {
		_, err := decodeInput(collection, input)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error=%v", err)
		}
	}
}
