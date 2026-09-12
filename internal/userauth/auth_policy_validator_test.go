package userauth

import (
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestServiceValidatesSchemaExecutableAuthPolicies(t *testing.T) {
	var validator schema.AuthExecutablePolicyValidator = (*Service)(nil)
	if !validator.ValidatePasswordPattern("^[A-Z]+$") || validator.ValidatePasswordPattern("[") {
		t.Fatal("password-pattern validation mismatch")
	}
	if !validator.ValidateMFARule(`{"field":"verified","equals":true}`) || validator.ValidateMFARule(`{"field":"invalid!","equals":true}`) {
		t.Fatal("MFA-rule validation mismatch")
	}
	if !validator.ValidateAuthRule("emailVisibility = true", nil) || validator.ValidateAuthRule("unknown = true", nil) {
		t.Fatal("auth-rule validation mismatch")
	}
}
