package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestLoadEnvRejectsShortTokenSecret(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL": "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 31)),
	}
	_, err := LoadEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "PGBASE_TOKEN_SECRET") {
		t.Fatalf("expected token-secret validation error, got %v", err)
	}
}

func TestLoadEnvParsesConfiguredValues(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL":         "postgresql://postgres:postgres@localhost/pgbase",
		"PGBASE_HTTP_ADDRESS":         "127.0.0.1:9000",
		"PGBASE_PUBLIC_URL":           "https://pgbase.example",
		"PGBASE_SETUP_TOKEN":          strings.Repeat("s", 32),
		"PGBASE_TOKEN_SECRET":         base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_AUTO_MIGRATE":         "true",
		"PGBASE_TRUSTED_PROXIES":      "10.0.0.0/8, 2001:db8::/32",
		"PGBASE_CORS_ALLOWED_ORIGINS": "https://admin.example,https://admin.example:8443",
		"PGBASE_ADMIN_SESSION_TTL":    "30m",
		"PGBASE_STATEMENT_TIMEOUT":    "7s",
		"PGBASE_SHUTDOWN_TIMEOUT":     "20s",
	}

	opts, err := LoadEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.HTTPAddress != "127.0.0.1:9000" || !opts.AutoMigrate {
		t.Fatalf("unexpected HTTP or migration settings: %#v", opts)
	}
	if got := opts.PublicURL.String(); got != "https://pgbase.example" {
		t.Fatalf("PublicURL = %q", got)
	}
	if len(opts.TrustedProxies) != 2 || opts.TrustedProxies[0].String() != "10.0.0.0/8" {
		t.Fatalf("TrustedProxies = %#v", opts.TrustedProxies)
	}
	if got := opts.CORSAllowedOrigins; len(got) != 2 || got[1] != "https://admin.example:8443" {
		t.Fatalf("CORSAllowedOrigins = %#v", got)
	}
	if opts.AdminSessionTTL != 30*time.Minute || opts.StatementTimeout != 7*time.Second || opts.ShutdownTimeout != 20*time.Second {
		t.Fatalf("unexpected duration settings: %#v", opts)
	}
}

func TestLoadEnvReportsInvalidParsingWithEnvironmentVariable(t *testing.T) {
	base := map[string]string{
		"PGBASE_DATABASE_URL": "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "boolean", key: "PGBASE_AUTO_MIGRATE", value: "sometimes"},
		{name: "duration", key: "PGBASE_ADMIN_SESSION_TTL", value: "later"},
		{name: "trusted proxy", key: "PGBASE_TRUSTED_PROXIES", value: "not-a-prefix"},
		{name: "CORS origin", key: "PGBASE_CORS_ALLOWED_ORIGINS", value: "https://example.com/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+1)
			for key, value := range base {
				env[key] = value
			}
			env[tt.key] = tt.value
			_, err := LoadEnv(func(key string) (string, bool) {
				value, ok := env[key]
				return value, ok
			})
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("expected %s parsing error, got %v", tt.key, err)
			}
		})
	}
}
