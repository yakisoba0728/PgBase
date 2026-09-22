package runtimesettings

import (
	"errors"
	"testing"
)

func TestAPIRateRulesRejectLabelsThatCannotDescribeRoutes(t *testing.T) {
	for _, label := range []string{"posts:list", "*:auth", "*", "api/records", " /api/", "/api/ ", "GET  /api/", "get /api/", "/api/?token=x", "/api/#fragment", "/api/\n", "/api/\x00", "/api/\u00a0"} {
		t.Run(label, func(t *testing.T) {
			settings := Default()
			settings.RateLimits.Rules = []APIRateRule{{Label: label, Duration: 60, MaxRequests: 5}}
			if err := validate(settings); !errors.Is(err, ErrInvalid) {
				t.Fatalf("label %q: error=%v, want invalid settings", label, err)
			}
		})
	}
}

func TestAPIRateRulesAcceptNativeRoutePatternsAndPrefixes(t *testing.T) {
	for _, label := range []string{"/", "/health/live", "/api/", "GET /api/", "PATCH /api/v1/collections/{collection}/records/{id}", "GET /reports/{path...}"} {
		t.Run(label, func(t *testing.T) {
			settings := Default()
			settings.RateLimits.Rules = []APIRateRule{{Label: label, Duration: 60, MaxRequests: 5}}
			if err := validate(settings); err != nil {
				t.Fatalf("valid route label %q: %v", label, err)
			}
		})
	}
}
