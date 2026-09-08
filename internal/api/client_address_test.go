package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientAddressIgnoresForwardingFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "203.0.113.10:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.99")
	got, ok := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if !ok || got != "203.0.113.10" {
		t.Fatalf("client address=%q valid=%t", got, ok)
	}
}

func TestClientAddressSelectsNearestUntrustedForwardedAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.99, 203.0.113.20, 10.0.0.4")
	got, ok := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if !ok || got != "203.0.113.20" {
		t.Fatalf("client address=%q valid=%t", got, ok)
	}
}

func TestClientAddressRejectsMalformedTrustedProxyChain(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, attacker-selected")
	got, ok := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if ok || got != "" {
		t.Fatalf("client address=%q valid=%t, want rejection", got, ok)
	}
}

func TestClientAddressFallsBackToPeerWhenEveryForwardedAddressIsTrusted(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "10.9.9.9, 10.0.0.4")
	got, ok := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if !ok || got != "10.0.0.5" {
		t.Fatalf("client address=%q valid=%t", got, ok)
	}
}

func TestClientAddressCoalescesIPv6SourcesWithinOnePrefix(t *testing.T) {
	addresses := []string{"2001:db8:1234:5678::1", "2001:db8:1234:5678:ffff::2"}
	var subject string
	for _, address := range addresses {
		request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
		request.RemoteAddr = "[" + address + "]:4321"
		got, ok := clientAddress(request, nil)
		if !ok {
			t.Fatalf("client address %q rejected", address)
		}
		if subject == "" {
			subject = got
		} else if got != subject {
			t.Fatalf("IPv6 subjects differ within /64: %q and %q", subject, got)
		}
	}
	if subject != "2001:db8:1234:5678::/64" {
		t.Fatalf("IPv6 subject = %q", subject)
	}
}
