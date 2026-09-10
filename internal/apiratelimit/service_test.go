package apiratelimit

import "testing"

func TestExcludedIPAcceptsPocketBaseAddressAndCIDRForms(t *testing.T) {
	if !excluded([]string{"203.0.113.0/24"}, "203.0.113.7") {
		t.Fatal("CIDR exclusion did not match")
	}
	if !excluded([]string{"2001:db8::1"}, "2001:db8::1") {
		t.Fatal("address exclusion did not match")
	}
	if excluded([]string{"203.0.113.0/24"}, "198.51.100.7") {
		t.Fatal("unrelated IP was excluded")
	}
}
