package userauth

import (
	"encoding/json"
	"testing"
)

func TestAuthCollectionRulesRejectUnknownKeysAndPreserveNull(t *testing.T) {
	var rules AuthCollectionRules
	if json.Unmarshal([]byte(`{"auth":null,"manage":"","unknown":"true"}`), &rules) == nil {
		t.Fatal("unknown rule key accepted")
	}
	if err := json.Unmarshal([]byte(`{"auth":null,"manage":""}`), &rules); err != nil || !rules.IsNull("auth") || rules.IsNull("manage") {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}
}
