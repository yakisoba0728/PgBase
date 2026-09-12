package runtimesettings

import (
	"errors"
	"sync"
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

func TestUpdatePublicationOwnsRateLimitSlicesAcrossInputsSnapshotsAndListeners(t *testing.T) {
	input := Default()
	input.RateLimits.Rules = []APIRateRule{{Label: "/api/", Duration: 60, MaxRequests: 10}}
	input.RateLimits.ExcludedIPs = []string{"127.0.0.1"}
	controller := &Controller{}
	var first, second Settings
	controller.Subscribe(func(snapshot Settings) {
		first = snapshot
		first.RateLimits.Rules[0].Label = "first-listener"
		first.RateLimits.ExcludedIPs[0] = "192.0.2.1"
	})
	controller.Subscribe(func(snapshot Settings) { second = snapshot })

	returned := controller.publish(input)
	input.RateLimits.Rules[0].Label = "caller-input"
	input.RateLimits.ExcludedIPs[0] = "198.51.100.1"
	returned.RateLimits.Rules[0].Label = "returned"
	returned.RateLimits.ExcludedIPs[0] = "203.0.113.1"

	got := controller.Get()
	if got.RateLimits.Rules[0].Label != "/api/" || got.RateLimits.ExcludedIPs[0] != "127.0.0.1" {
		t.Fatalf("published settings were mutated: %#v", got.RateLimits)
	}
	if second.RateLimits.Rules[0].Label != "/api/" || second.RateLimits.ExcludedIPs[0] != "127.0.0.1" {
		t.Fatalf("listener snapshots share storage: %#v", second.RateLimits)
	}

	got.RateLimits.Rules[0].Label = "get-result"
	got.RateLimits.ExcludedIPs[0] = "203.0.113.2"
	if again := controller.Get(); again.RateLimits.Rules[0].Label != "/api/" || again.RateLimits.ExcludedIPs[0] != "127.0.0.1" {
		t.Fatalf("Get result mutated the published settings: %#v", again.RateLimits)
	}
}

func TestGetRateLimitSnapshotsAreSafeForConcurrentCallerMutation(t *testing.T) {
	controller := &Controller{value: Default()}
	controller.value.RateLimits.Rules = []APIRateRule{{Label: "/api/", Duration: 60, MaxRequests: 10}}
	controller.value.RateLimits.ExcludedIPs = []string{"127.0.0.1"}

	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				snapshot := controller.Get()
				snapshot.RateLimits.Rules[0].Label = "local"
				snapshot.RateLimits.ExcludedIPs[0] = "192.0.2.1"
			}
		}()
	}
	workers.Wait()
	if got := controller.Get(); got.RateLimits.Rules[0].Label != "/api/" || got.RateLimits.ExcludedIPs[0] != "127.0.0.1" {
		t.Fatalf("concurrent snapshot mutations changed live settings: %#v", got.RateLimits)
	}
}
