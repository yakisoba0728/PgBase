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
