package query

import (
	"encoding/json"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
)

func TestNativeRuleSourceReferencesInsideJSONStringsRemainLiteral(t *testing.T) {
	for _, literal := range []string{
		`@request.auth.id`,
		`@request.body.title = "x" || @collection.notes.id != ''`,
		`backslash\ and "quote"`,
	} {
		encoded, err := json.Marshal(literal)
		if err != nil {
			t.Fatal(err)
		}
		source := "title = " + string(encoded)
		rule, err := ParseAccessRule(source)
		if err != nil || rule.Binding != accessrule.BindingCatalog || rule.Field != "title" || string(rule.Value) != string(encoded) || rule.Expression == nil || *rule.Expression != source {
			t.Fatalf("literal was rewritten: source=%s rule=%#v err=%v", source, rule, err)
		}
	}
}

func TestNativePolicyInputProducesExplicitBindingAndRoundTrips(t *testing.T) {
	rules, err := ParseAccessRules([]byte(`{"list":{"op":"expression","expression":"title = 'published'"},"view":{"op":"owner"},"create":{"op":"deny"},"update":{"op":"owner"},"delete":{"op":"deny"}}`))
	if err != nil || rules.List.Binding != accessrule.BindingCatalog || rules.List.Op != "eq" {
		t.Fatalf("native input did not bind: %#v %v", rules, err)
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseAccessRules(encoded)
	if err != nil || restored.List.Binding != accessrule.BindingCatalog || restored.View.Op != "owner" {
		t.Fatalf("policy roundtrip lost binding: %#v %v", restored, err)
	}
}
