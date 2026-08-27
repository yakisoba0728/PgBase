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
	got := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got != "203.0.113.10" {
		t.Fatalf("client address=%q", got)
	}
}

func TestClientAddressSelectsNearestUntrustedForwardedAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.99, 203.0.113.20, 10.0.0.4")
	got := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got != "203.0.113.20" {
		t.Fatalf("client address=%q", got)
	}
}

func TestClientAddressFallsBackToTrustedPeerForMalformedChain(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, attacker-selected")
	got := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got != "10.0.0.5" {
		t.Fatalf("client address=%q", got)
	}
}

func TestClientAddressFallsBackToPeerWhenEveryForwardedAddressIsTrusted(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "10.0.0.5:4321"
	request.Header.Set("X-Forwarded-For", "10.9.9.9, 10.0.0.4")
	got := clientAddress(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got != "10.0.0.5" {
		t.Fatalf("client address=%q", got)
	}
}
