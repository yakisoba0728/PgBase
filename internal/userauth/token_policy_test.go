package userauth

import "testing"

func TestTokenPolicyVersionIsURLSafe(t *testing.T) {
	for _, valid := range []string{"v1", "release_2026-09", "ABC123"} {
		if !validTokenPolicyVersion(valid) {
			t.Errorf("validTokenPolicyVersion(%q) = false", valid)
		}
	}
	for _, invalid := range []string{"", "has.dot", "has space", "line\nbreak", "é", "123456789012345678901234567890123"} {
		if validTokenPolicyVersion(invalid) {
			t.Errorf("validTokenPolicyVersion(%q) = true", invalid)
		}
	}
}
