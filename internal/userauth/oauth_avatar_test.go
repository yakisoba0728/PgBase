package userauth

import (
	"net"
	"net/url"
	"testing"
)

func TestAvatarURLPolicy(t *testing.T) {
	for _, test := range []struct {
		raw            string
		loopback, want bool
	}{
		{"https://images.example/avatar.png", false, true},
		{"http://images.example/avatar.png", false, false},
		{"http://127.0.0.1:8080/avatar.png", true, true},
		{"https://user:secret@images.example/avatar.png", false, false},
	} {
		u, _ := url.Parse(test.raw)
		if got := avatarURLAllowed(u, test.loopback); got != test.want {
			t.Errorf("avatarURLAllowed(%q)=%v want %v", test.raw, got, test.want)
		}
	}
	if publicIP(net.ParseIP("10.0.0.1")) || publicIP(net.ParseIP("127.0.0.1")) || !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP classification is unsafe")
	}
}
