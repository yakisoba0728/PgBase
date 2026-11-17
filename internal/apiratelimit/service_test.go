package apiratelimit

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

func TestExcludedIPAcceptsAddressAndCIDRForms(t *testing.T) {
	for _, tc := range []struct {
		values []string
		ip     string
		want   bool
	}{
		{[]string{"203.0.113.0/24"}, "203.0.113.7", true},
		{[]string{"2001:db8::1"}, "2001:db8::1", true},
		{[]string{"203.0.113.0/24"}, "198.51.100.7", false},
		{[]string{"invalid", "2001:db8::/32"}, "2001:db8::9", true},
		{[]string{"203.0.113.7"}, "203.0.113.7", true},
		{[]string{"203.0.113.7"}, "203.0.113.8", false},
		{[]string{"0.0.0.0/0"}, "invalid", false},
		{[]string{"203.0.113.0/24"}, "::ffff:203.0.113.7", false},
		{nil, "203.0.113.7", false},
	} {
		if got := compileExclusions(tc.values).contains(tc.ip); got != tc.want {
			t.Errorf("contains(%q, %q)=%v, want %v", tc.values, tc.ip, got, tc.want)
		}
	}
}

func TestSetCopiesExcludedIPsSnapshot(t *testing.T) {
	settings := runtimesettings.Default()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/", Duration: 60, MaxRequests: 1}}
	settings.RateLimits.ExcludedIPs = []string{"203.0.113.7"}
	service := New(nil, nil, settings)
	settings.RateLimits.ExcludedIPs[0] = "198.51.100.7"
	settings.RateLimits.Rules[0].Label = "/changed"
	if service.settings.Load().rules[0].Label != "/" {
		t.Fatal("published rules changed after caller mutation")
	}
	decision, err := service.Admit(context.Background(), "GET", "/", "", "203.0.113.7")
	if err != nil || !decision.Allowed {
		t.Fatalf("published exclusion changed after caller mutation: decision=%+v err=%v", decision, err)
	}
}

func TestConcurrentPolicyPublication(t *testing.T) {
	settings := runtimesettings.Default()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/", Duration: 60, MaxRequests: 1}}
	settings.RateLimits.ExcludedIPs = []string{"203.0.113.0/24"}
	service := New(nil, nil, settings)
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 200; i++ {
				service.Set(settings)
				decision, err := service.Admit(context.Background(), "GET", "/", "", "203.0.113.7")
				if err != nil || !decision.Allowed {
					t.Errorf("published policy unavailable: %+v, %v", decision, err)
					return
				}
			}
		}()
	}
	workers.Wait()
}

// Baseline reference kept only in the benchmark: exactly the old per-request
// parsing algorithm, so both implementations run in the same process and host.
func uncompiledExcluded(values []string, ip string) bool {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Contains(address) {
			return true
		}
		if candidate, err := netip.ParseAddr(value); err == nil && candidate == address {
			return true
		}
	}
	return false
}

func BenchmarkExcludedIPPolicy(b *testing.B) {
	for _, count := range []int{1, 32, 256} {
		values := make([]string, count)
		for i := range values {
			values[i] = fmt.Sprintf("10.%d.0.0/16", i)
		}
		ip := fmt.Sprintf("10.%d.1.1", count-1)
		compiled := compileExclusions(values)
		b.Run(fmt.Sprintf("networks=%d/baseline", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !uncompiledExcluded(values, ip) {
					b.Fatal("baseline did not match")
				}
			}
		})
		b.Run(fmt.Sprintf("networks=%d/compiled", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !compiled.contains(ip) {
					b.Fatal("compiled policy did not match")
				}
			}
		})
	}
}
