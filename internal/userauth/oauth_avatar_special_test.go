package userauth

import (
	"net"
	"testing"
)

func TestOAuthAvatarRejectsSpecialUseAddresses(t *testing.T) {
	for _, address := range []string{
		"0.1.2.3", "100.64.0.1", "100.127.255.254", "192.0.0.8",
		"192.0.2.1", "192.88.99.2", "198.18.0.1", "198.19.255.254",
		"198.51.100.1", "203.0.113.1", "240.0.0.1", "255.255.255.255",
		"::ffff:100.64.0.1", "64:ff9b::a00:1", "64:ff9b:1::a00:1",
		"100::1", "100:0:0:1::1", "2001::1", "2001:2::1",
		"2001:db8::1", "2002:a00:1::1", "3fff::1", "5f00::1", "fec0::1",
	} {
		t.Run(address, func(t *testing.T) {
			if publicIP(net.ParseIP(address)) {
				t.Fatalf("special-use destination accepted: %s", address)
			}
		})
	}
	for _, invalid := range []net.IP{nil, {}, {1, 2, 3}} {
		if publicIP(invalid) {
			t.Errorf("invalid IP accepted: %v", invalid)
		}
	}
}

func TestOAuthAvatarPreservesPublicAddressControls(t *testing.T) {
	for _, address := range []string{
		"8.8.8.8", "1.1.1.1", "100.63.255.254", "100.128.0.1",
		"198.17.255.254", "198.20.0.1", "::ffff:8.8.8.8",
		"2001:4860:4860::8888", "2606:4700:4700::1111",
	} {
		if !publicIP(net.ParseIP(address)) {
			t.Errorf("ordinary public destination rejected: %s", address)
		}
	}
}
