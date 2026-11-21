package runtimesettings

import (
	"net/netip"
	"reflect"
	"testing"
)

// TrustedProxies is parsed once per publication; every caller still gets its
// own slice, unparseable entries are skipped as before, and a later
// publication replaces the cached prefixes.
func TestTrustedProxiesFollowPublicationsAndStayCallerOwned(t *testing.T) {
	controller := &Controller{}
	if got := controller.TrustedProxies(); got == nil || len(got) != 0 {
		t.Fatalf("unpublished proxies = %#v, want empty non-nil", got)
	}
	settings := Default()
	settings.Proxy.TrustedProxyCIDRs = []string{"10.0.0.0/8", "not-a-prefix", "2001:db8::/32"}
	controller.publish(settings)
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	first := controller.TrustedProxies()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("proxies = %v, want %v", first, want)
	}
	first[0] = netip.MustParsePrefix("192.0.2.0/24")
	if again := controller.TrustedProxies(); !reflect.DeepEqual(again, want) {
		t.Fatalf("caller mutation leaked into the cache: %v", again)
	}

	settings.Proxy.TrustedProxyCIDRs = nil
	controller.publish(settings)
	if got := controller.TrustedProxies(); got == nil || len(got) != 0 {
		t.Fatalf("republished proxies = %#v, want empty non-nil", got)
	}
}
