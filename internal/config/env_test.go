package config

import (
	"bytes"
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

func TestLoadEnvReadsPublicStaticSettings(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL":        "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET":        base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_PUBLIC_DIR":          "/srv/pgbase/public",
		"PGBASE_PUBLIC_SPA_FALLBACK": "true",
	}
	options, err := LoadEnv(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if options.PublicDir != "/srv/pgbase/public" || !options.PublicSPAFallback {
		t.Fatalf("public options=%#v", options)
	}
}

func TestLoadEnvReadsRuntimeSettingsEncryptionKey(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	env := map[string]string{
		"PGBASE_DATABASE_URL":                    "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET":                    base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_RUNTIME_SETTINGS_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(key),
	}
	options, err := LoadEnv(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(options.RuntimeSettingsEncryptionKey, key) {
		t.Fatalf("RuntimeSettingsEncryptionKey = %q", options.RuntimeSettingsEncryptionKey)
	}
}

func TestLoadEnvBackupS3IsIndependentFromFileStorage(t *testing.T) {
	values := map[string]string{
		"PGBASE_DATABASE_URL":      "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET":      base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_BACKUPS_DIRECTORY": "/tmp/backup-scratch",
		"PGBASE_BACKUPS_S3_BUCKET": "backup-bucket", "PGBASE_BACKUPS_S3_PREFIX": "daily",
		"PGBASE_BACKUPS_S3_REGION": "us-east-1", "PGBASE_BACKUPS_S3_ENDPOINT": "http://127.0.0.1:59000",
		"PGBASE_BACKUPS_S3_ACCESS_KEY": "backup-access", "PGBASE_BACKUPS_S3_SECRET_KEY": "backup-secret",
		"PGBASE_BACKUPS_S3_PATH_STYLE": "true",
		"PGBASE_BACKUPS_RETENTION":     "14",
	}
	options, err := LoadEnv(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if options.Backups.S3.Bucket != "backup-bucket" || options.Backups.S3.Prefix != "daily" || !options.Backups.S3.UsePathStyle || options.Backups.Retention != 14 {
		t.Fatalf("backup S3=%+v", options.Backups.S3)
	}
	if options.Files.S3Bucket != "" {
		t.Fatalf("file storage inherited backup bucket: %+v", options.Files)
	}
}

func TestLoadEnvRejectsSetupTokenThatCannotFitTheHTTPBudget(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL": "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_SETUP_TOKEN":  strings.Repeat("s", 1025),
	}
	_, err := LoadEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "SetupToken") {
		t.Fatalf("expected setup-token HTTP-budget validation error, got %v", err)
	}
}

func TestLoadEnvParsesConfiguredValues(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL":                   "postgresql://postgres:postgres@localhost/pgbase",
		"PGBASE_HTTP_ADDRESS":                   "127.0.0.1:9000",
		"PGBASE_PUBLIC_URL":                     "https://pgbase.example",
		"PGBASE_SETUP_TOKEN":                    strings.Repeat("s", 32),
		"PGBASE_METRICS_TOKEN":                  strings.Repeat("m", 32),
		"PGBASE_TOKEN_SECRET":                   base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_ALLOW_INSECURE_DATABASE":        "true",
		"PGBASE_AUTO_MIGRATE":                   "true",
		"PGBASE_USER_MIGRATION_AUTO_RECORD_DIR": "/tmp/pgbase-user-migrations",
		"PGBASE_USER_REGISTRATION":              "true",
		"PGBASE_TRUSTED_PROXIES":                "10.0.0.0/8, 2001:db8::/32",
		"PGBASE_CORS_ALLOWED_ORIGINS":           "https://admin.example,https://admin.example:8443",
		"PGBASE_ADMIN_SESSION_TTL":              "30m",
		"PGBASE_STATEMENT_TIMEOUT":              "7s",
		"PGBASE_SHUTDOWN_TIMEOUT":               "20s",
		"PGBASE_SCHEMA_APPLY_TIMEOUT":           "12m",
		"PGBASE_STOP_GRACE_PERIOD":              "25s",
	}

	opts, err := LoadEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.HTTPAddress != "127.0.0.1:9000" || !opts.AutoMigrate || !opts.AllowInsecureDatabase {
		t.Fatalf("unexpected HTTP or migration settings: %#v", opts)
	}
	if opts.UserMigrationAutoRecordDirectory != "/tmp/pgbase-user-migrations" {
		t.Fatalf("UserMigrationAutoRecordDirectory = %q", opts.UserMigrationAutoRecordDirectory)
	}
	if got := opts.PublicURL.String(); got != "https://pgbase.example" {
		t.Fatalf("PublicURL = %q", got)
	}
	if len(opts.TrustedProxies) != 2 || opts.TrustedProxies[0].String() != "10.0.0.0/8" {
		t.Fatalf("TrustedProxies = %#v", opts.TrustedProxies)
	}
	if !opts.UserRegistrationEnabled {
		t.Fatal("user registration opt-in not loaded")
	}
	if got := opts.CORSAllowedOrigins; len(got) != 2 || got[1] != "https://admin.example:8443" {
		t.Fatalf("CORSAllowedOrigins = %#v", got)
	}
	if opts.AdminSessionTTL != 30*time.Minute || opts.StatementTimeout != 7*time.Second ||
		opts.ShutdownTimeout != 20*time.Second || opts.SchemaApplyTimeout != 12*time.Minute {
		t.Fatalf("unexpected duration settings: %#v", opts)
	}
	if string(opts.MetricsToken) != strings.Repeat("m", 32) {
		t.Fatalf("MetricsToken = %q", opts.MetricsToken)
	}
}

func TestLoadEnvEnforcesRemoteDatabaseTransportUnlessExplicitlyOptedOut(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		databaseURL   string
		allowInsecure string
		wantErr       bool
	}{
		{
			name:        "remote plaintext rejected",
			databaseURL: "postgres://postgres:postgres@db.example/pgbase?sslmode=disable",
			wantErr:     true,
		},
		{
			name:        "remote verified TLS accepted",
			databaseURL: "postgres://postgres:postgres@db.example/pgbase?sslmode=verify-full",
		},
		{
			name:          "explicit development opt-in accepted",
			databaseURL:   "postgres://postgres:postgres@postgres/pgbase?sslmode=disable",
			allowInsecure: "true",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := map[string]string{
				"PGBASE_DATABASE_URL": testCase.databaseURL,
				"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 32)),
			}
			if testCase.allowInsecure != "" {
				env["PGBASE_ALLOW_INSECURE_DATABASE"] = testCase.allowInsecure
			}
			_, err := LoadEnv(func(key string) (string, bool) {
				value, ok := env[key]
				return value, ok
			})
			if testCase.wantErr && (err == nil || !strings.Contains(err.Error(), "sslmode=verify-full")) {
				t.Fatalf("LoadEnv() error = %v, want verified-TLS policy rejection", err)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("LoadEnv() error = %v, want nil", err)
			}
		})
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
		{name: "registration boolean", key: "PGBASE_USER_REGISTRATION", value: "sometimes"},
		{name: "insecure database boolean", key: "PGBASE_ALLOW_INSECURE_DATABASE", value: "sometimes"},
		{name: "duration", key: "PGBASE_ADMIN_SESSION_TTL", value: "later"},
		{name: "stop grace duration", key: "PGBASE_STOP_GRACE_PERIOD", value: "later"},
		{name: "trusted proxy", key: "PGBASE_TRUSTED_PROXIES", value: "not-a-prefix"},
		{name: "all IPv4 trusted proxy", key: "PGBASE_TRUSTED_PROXIES", value: "0.0.0.0/0"},
		{name: "all IPv6 trusted proxy", key: "PGBASE_TRUSTED_PROXIES", value: "::/0"},
		{name: "CORS origin", key: "PGBASE_CORS_ALLOWED_ORIGINS", value: "https://example.com/path"},
		{name: "public origin empty fragment", key: "PGBASE_PUBLIC_URL", value: "https://example.com#"},
		{name: "CORS origin empty fragment", key: "PGBASE_CORS_ALLOWED_ORIGINS", value: "https://example.com#"},
		{name: "short metrics token", key: "PGBASE_METRICS_TOKEN", value: strings.Repeat("m", 31)},
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

func TestLoadEnvRequiresComposeStopGraceMargin(t *testing.T) {
	base := map[string]string{
		"PGBASE_DATABASE_URL":     "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET":     base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"PGBASE_SHUTDOWN_TIMEOUT": "20s",
	}
	tests := []struct {
		name    string
		grace   string
		wantErr bool
	}{
		{name: "exact five second margin", grace: "25s"},
		{name: "larger margin", grace: "1m"},
		{name: "short by one nanosecond", grace: "24.999999999s", wantErr: true},
		{name: "same as shutdown", grace: "20s", wantErr: true},
		{name: "negative", grace: "-1s", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+1)
			for key, value := range base {
				env[key] = value
			}
			env["PGBASE_STOP_GRACE_PERIOD"] = tt.grace
			_, err := LoadEnv(func(key string) (string, bool) {
				value, ok := env[key]
				return value, ok
			})
			if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "PGBASE_STOP_GRACE_PERIOD")) {
				t.Fatalf("LoadEnv() error = %v, want stop-grace policy error", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("LoadEnv() error = %v, want nil", err)
			}
		})
	}
}
