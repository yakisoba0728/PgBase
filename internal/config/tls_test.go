package config

import (
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestLoadEnvConfiguresExplicitACMEHTTPS(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(make([]byte, 32))
	values := map[string]string{
		"PGBASE_DATABASE_URL":            "postgres://pgbase:password@localhost:5432/pgbase?sslmode=disable",
		"PGBASE_TOKEN_SECRET":            secret,
		"PGBASE_ALLOW_INSECURE_DATABASE": "true",
		"PGBASE_HTTPS_ADDRESS":           "127.0.0.1:8443",
		"PGBASE_ACME_HOSTS":              "pgbase.example",
		"PGBASE_ACME_CACHE_DIR":          filepath.Join(t.TempDir(), "acme"),
		"PGBASE_ACME_HTTP_ADDRESS":       "127.0.0.1:8080",
	}
	options, err := LoadEnv(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if options.TLS == nil || options.TLS.Address != "127.0.0.1:8443" || len(options.TLS.ACMEHosts) != 1 {
		t.Fatalf("TLS=%+v", options.TLS)
	}
}
