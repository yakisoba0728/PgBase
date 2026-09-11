package userauth

import "github.com/yakisoba0728/PgBase/internal/schema"

// ValidatePasswordPattern implements schema.AuthExecutablePolicyValidator.
func (*Service) ValidatePasswordPattern(pattern string) bool {
	return validPasswordPattern(pattern)
}

// ValidateMFARule implements schema.AuthExecutablePolicyValidator.
func (*Service) ValidateMFARule(rule string) bool {
	return validMFARule(rule)
}

// ValidateAuthRule implements schema.AuthExecutablePolicyValidator.
func (*Service) ValidateAuthRule(rule string, fields []schema.FieldInput) bool {
	return ValidateAuthRecordRules(AuthCollectionRules{View: rule}, fields)
}
