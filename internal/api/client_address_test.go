package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
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

func TestClientIPDropsTheIPv6ZoneOfTheRemoteAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
	request.RemoteAddr = "[fe80::1%en0]:4321"
	if got, ok := clientIP(request, nil); !ok || got != "fe80::1" {
		t.Fatalf("client IP=%q valid=%t", got, ok)
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

func TestIgnoredForwardingNamesTheMisconfiguration(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, test := range []struct {
		name, peer, header, value, want string
	}{
		{"no forwarding headers", "172.18.0.5:1", "", "", ""},
		{"untrusted proxy", "172.18.0.5:1", "X-Forwarded-For", "198.51.100.9", "untrusted_peer"},
		{"untrusted proxy with Forwarded", "172.18.0.5:1", "Forwarded", "for=198.51.100.9", "untrusted_peer"},
		{"untrusted proxy with X-Real-IP", "172.18.0.5:1", "X-Real-IP", "198.51.100.9", "untrusted_peer"},
		{"trusted proxy", "10.0.0.5:1", "X-Forwarded-For", "198.51.100.9", ""},
		{"trusted proxy without X-Forwarded-For", "10.0.0.5:1", "Forwarded", "for=198.51.100.9", "unread_header"},
		{"unparseable peer", "pipe", "X-Forwarded-For", "198.51.100.9", ""},
	} {
		request := httptest.NewRequest("GET", "http://pgbase.internal", nil)
		request.RemoteAddr = test.peer
		if test.header != "" {
			request.Header.Set(test.header, test.value)
		}
		if got, _ := ignoredForwarding(request, trusted); got != test.want {
			t.Errorf("%s: reason = %q, want %q", test.name, got, test.want)
		}
	}
}

// Behind an untrusted reverse proxy every anonymous client shares the proxy's
// buckets. The router warns once per misconfiguration, names a private peer
// exactly so it can be trusted, and shows only the network of a public peer.
func TestRouterWarnsOnceAboutIgnoredForwardingHeaders(t *testing.T) {
	var logs bytes.Buffer
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	handler := mustRouter(t, Dependencies{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), TrustedProxies: trusted})
	send := func(peer, header, value string) {
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.RemoteAddr = peer
		if header != "" {
			request.Header.Set(header, value)
		}
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	send("172.18.0.5:1", "", "")
	send("10.0.0.5:1", "X-Forwarded-For", "198.51.100.9")
	if strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("warned without a misconfiguration:\n%s", logs.String())
	}
	send("172.18.0.5:1", "X-Forwarded-For", "198.51.100.9")
	send("172.18.0.6:1", "X-Forwarded-For", "198.51.100.10")
	send("10.0.0.5:1", "Forwarded", "for=198.51.100.9")
	send("10.0.0.5:1", "X-Real-IP", "198.51.100.9")
	if got := strings.Count(logs.String(), `"code":"forwarding_headers_untrusted_peer"`); got != 1 {
		t.Fatalf("untrusted peer warnings = %d, want 1:\n%s", got, logs.String())
	}
	if got := strings.Count(logs.String(), `"code":"forwarding_header_unread"`); got != 1 {
		t.Fatalf("unread header warnings = %d, want 1:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), `"peer":"172.18.0.5"`) {
		t.Fatalf("warning does not name the private peer:\n%s", logs.String())
	}
	for peer, want := range map[string]string{"203.0.113.77": "203.0.113.0/24", "2001:db8:1:2:3::4": "2001:db8:1:2::/64", "127.0.0.1": "127.0.0.1"} {
		if got := forwardingPeer(netip.MustParseAddr(peer)); got != want {
			t.Errorf("forwardingPeer(%s) = %q, want %q", peer, got, want)
		}
	}
}
