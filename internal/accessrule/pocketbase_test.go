package accessrule

import (
	"encoding/json"
	"testing"
)

func TestPocketBaseRulesPreserveNullAndEmpty(t *testing.T) {
	empty := ""
	wire := PocketBaseRules{List: nil, View: &empty, Create: nil, Update: &empty, Delete: nil}
	rules, err := FromPocketBaseRules(wire)
	if err != nil {
		t.Fatal(err)
	}
	if rules.List.Op != "deny" || rules.View.Op != "allow" {
		t.Fatalf("rules=%+v", rules)
	}
	roundtrip := rules.PocketBase()
	raw, err := json.Marshal(roundtrip)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"listRule":null,"viewRule":"","createRule":null,"updateRule":"","deleteRule":null}` {
		t.Fatalf("wire=%s", raw)
	}
}

func TestPocketBaseRulesRejectUnsupportedText(t *testing.T) {
	unsupported := "title = 'x'"
	_, err := FromPocketBaseRules(PocketBaseRules{List: &unsupported})
	if err != ErrInvalid {
		t.Fatalf("error=%v", err)
	}
}

func TestPocketBaseRulesParsedPreserveExactValidatedText(t *testing.T) {
	source := " title = null || title = '' "
	rules, err := FromPocketBaseRulesParsed(PocketBaseRules{List: &source}, func(text string) (Rule, error) { return Rule{Op: "allow", PocketBase: &text}, nil })
	if err != nil || rules.PocketBase().List == nil || *rules.PocketBase().List != source {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}
}
