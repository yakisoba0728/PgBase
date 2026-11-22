package adminauth

import (
	"bytes"
	"testing"
)

func TestNetworkRateLimitSubjectAggregatesIPv6To64(t *testing.T) {
	for _, tc := range []struct{ subject, want string }{
		{"2001:db8:1:1::1", "2001:db8:1:1::/64"},
		{"2001:db8:1:1:ffff:ffff:ffff:ffff", "2001:db8:1:1::/64"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:1::/64", "2001:db8:1:1::/64"},
		{"192.0.2.10", "192.0.2.10"},
		{"::ffff:192.0.2.10", "192.0.2.10"},
		{"fe80::1%eth0", "fe80::1%eth0"},
		{"unknown", "unknown"},
		{"overflow-network", "overflow-network"},
	} {
		if got := networkRateLimitSubject(tc.subject); got != tc.want {
			t.Errorf("networkRateLimitSubject(%q) = %q, want %q", tc.subject, got, tc.want)
		}
	}
}

func TestRateLimitSubjectHashSharesNetworkBucketAcrossOne64(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	first := rateLimitSubjectHash(secret, "login", "network", "2001:db8:1:1::1")
	second := rateLimitSubjectHash(secret, "login", "network", "2001:db8:1:1::2")
	prefix := rateLimitSubjectHash(secret, "login", "network", "2001:db8:1:1::/64")
	other := rateLimitSubjectHash(secret, "login", "network", "2001:db8:1:2::1")
	if !bytes.Equal(first, second) || !bytes.Equal(first, prefix) {
		t.Fatal("addresses in one IPv6 /64 must share a network bucket")
	}
	if bytes.Equal(first, other) {
		t.Fatal("distinct IPv6 /64 networks must not share a network bucket")
	}
	// Account buckets keep their exact subject.
	if bytes.Equal(rateLimitSubjectHash(secret, "login", "account", "2001:db8:1:1::1"), rateLimitSubjectHash(secret, "login", "account", "2001:db8:1:1::2")) {
		t.Fatal("non-network axes must not be aggregated")
	}
	// IPv4 hashing is unchanged.
	if !bytes.Equal(rateLimitSubjectHash(secret, "login", "network", "192.0.2.1"), tokenHMAC(secret, "rate-limit\x00login\x00network\x00", "192.0.2.1")) {
		t.Fatal("IPv4 network bucket hash changed")
	}
}
