package schema

import (
	"reflect"
	"testing"
)

func TestN4TextOptionsCanonicalizeAtPlanSeam(t *testing.T) {
	change := NewCreateChange(CreateCollectionInput{Name: "generated_items", Fields: []FieldInput{{
		Name: "link", Type: TypeText, Unique: true, Options: &FieldOptions{
			AutogeneratePattern: `https://[a-z]{8}[.]example[.]com`,
			Format:              "url",
			AllowedDomains:      []string{" Docs.Example.COM. ", "example.com"},
			DeniedDomains:       []string{" Private.Example.com. "},
		},
	}}})
	plan, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, change)
	if err != nil {
		t.Fatal(err)
	}
	options := plan.Definition.Fields[0].Options
	if !reflect.DeepEqual(options.AllowedDomains, []string{"docs.example.com", "example.com"}) ||
		!reflect.DeepEqual(options.DeniedDomains, []string{"private.example.com"}) ||
		options.AutogeneratePattern != `https://[a-z]{8}[.]example[.]com` {
		t.Fatalf("canonical options=%#v", options)
	}
}

func TestN4TextOptionsRejectAmbiguousOrUnsafeDefinitions(t *testing.T) {
	tests := []FieldInput{
		{Name: "number", Type: TypeInteger, Options: &FieldOptions{AutogeneratePattern: `[0-9]{8}`}},
		{Name: "defaulted", Type: TypeText, Default: []byte(`"fixed"`), Options: &FieldOptions{AutogeneratePattern: `[a-z]{8}`}},
		{Name: "unbounded", Type: TypeText, Options: &FieldOptions{AutogeneratePattern: `^value$`}},
		{Name: "star", Type: TypeText, Options: &FieldOptions{AutogeneratePattern: `a*`}},
		{Name: "plus", Type: TypeText, Options: &FieldOptions{AutogeneratePattern: `a+`}},
		{Name: "open_repeat", Type: TypeText, Options: &FieldOptions{AutogeneratePattern: `a{3,}`}},
		{Name: "domains", Type: TypeText, Options: &FieldOptions{DeniedDomains: []string{"example.com"}}},
		{Name: "bad_domain", Type: TypeText, Options: &FieldOptions{Format: "email", DeniedDomains: []string{"bad_domain"}}},
	}
	for _, field := range tests {
		_, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(CreateCollectionInput{Name: "invalid_" + field.Name, Fields: []FieldInput{field}}))
		if err == nil {
			t.Errorf("accepted %#v", field)
		}
	}
}
