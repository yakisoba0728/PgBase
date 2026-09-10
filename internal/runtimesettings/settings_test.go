package runtimesettings

import (
	"errors"
	"testing"
)

func TestDefaultRatePoliciesAreValidatedAndBounded(t *testing.T) {
	settings := Default()
	if err := validate(settings); err != nil {
		t.Fatal(err)
	}
	settings.RateLimits.User.WindowSeconds = 0
	if err := validate(settings); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero window error = %v", err)
	}
	settings = Default()
	settings.RateLimits.Admin.NetworkMaxFailures = 10001
	if err := validate(settings); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded threshold error = %v", err)
	}
}
