package userauth

import "testing"

func TestAuthRecordRuleValidation(t *testing.T) {
	for _, rule := range []string{"", "true", "false", "emailVisibility = true"} {
		if !ValidateAuthRecordRules(AuthCollectionRules{View: rule}, nil) {
			t.Fatalf("valid auth rule rejected: %q", rule)
		}
	}
	if ValidateAuthRecordRules(AuthCollectionRules{View: "missing = true"}, nil) {
		t.Fatal("unknown auth rule field accepted")
	}
}
