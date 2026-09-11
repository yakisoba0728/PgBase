package pgbase

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTLSOptionsRejectAmbiguousOrUnsafeACMEConfiguration(t *testing.T) {
	for _, test := range []struct {
		name  string
		value TLSOptions
	}{
		{"missing mode", TLSOptions{Address: "127.0.0.1:443"}},
		{"missing challenge", TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{"pgbase.example"}, ACMECacheDirectory: t.TempDir()}},
		{"wildcard host", TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{"*.example.com"}, ACMECacheDirectory: t.TempDir(), HTTPChallengeAddress: "127.0.0.1:80"}},
		{"relative cache", TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{"pgbase.example"}, ACMECacheDirectory: "cache", HTTPChallengeAddress: "127.0.0.1:80"}},
		{"remote insecure CA", TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{"pgbase.example"}, ACMECacheDirectory: t.TempDir(), HTTPChallengeAddress: "127.0.0.1:80", ACMEDirectoryURL: "http://acme.example/directory"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.value.normalized(); err == nil {
				t.Fatal("accepted unsafe TLS configuration")
			}
		})
	}
}

func TestTLSOptionsNormalizeExplicitACMEAllowlist(t *testing.T) {
	value, err := (TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{"PgBase.Example."}, ACMECacheDirectory: filepath.Join(t.TempDir(), "cache"), HTTPChallengeAddress: "127.0.0.1:80"}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if len(value.ACMEHosts) != 1 || value.ACMEHosts[0] != "pgbase.example" {
		t.Fatalf("ACME hosts=%q", value.ACMEHosts)
	}
}

func TestTLSOptionsRejectsInvalidACMEHostnames(t *testing.T) {
	invalid := []string{"example..com", "-api.example.com", "api-.example.com", "api_example.com", strings.Repeat("a", 64) + ".example", strings.Repeat("a.", 127) + "com"}
	for _, host := range invalid {
		t.Run(host, func(t *testing.T) {
			_, err := (TLSOptions{Address: "127.0.0.1:443", ACMEHosts: []string{host}, ACMECacheDirectory: t.TempDir(), HTTPChallengeAddress: "127.0.0.1:80"}).normalized()
			if err == nil {
				t.Fatalf("accepted invalid ACME hostname %q", host)
			}
		})
	}
}
