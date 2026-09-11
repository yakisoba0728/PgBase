package apiratelimit

import (
	"context"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

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

func TestSetCopiesExcludedIPsSnapshot(t *testing.T) {
	settings := runtimesettings.Default()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/", Duration: 60, MaxRequests: 1}}
	settings.RateLimits.ExcludedIPs = make([]string, 1, 2)
	settings.RateLimits.ExcludedIPs[0] = "203.0.113.7"
	service := New(nil, nil, settings)
	settings.RateLimits.ExcludedIPs[0] = "198.51.100.7"

	decision, err := service.Admit(context.Background(), "GET", "/", "", "203.0.113.7")
	if err != nil || !decision.Allowed {
		t.Fatalf("published exclusion changed after caller mutation: decision=%+v err=%v", decision, err)
	}
}
