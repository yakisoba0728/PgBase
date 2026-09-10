// Package config loads pgBase settings from environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/pgbase"
)

const runtimeSettingsEncryptionEnvSelector = "PGBASE_RUNTIME_SETTINGS_ENCRYPTION_ENV"
const defaultRuntimeSettingsEncryptionEnv = "PGBASE_RUNTIME_SETTINGS_ENCRYPTION_KEY"

var environmentName = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_]*\z`)

func LoadEnv(lookup func(string) (string, bool)) (pgbase.Options, error) {
	if configured, ok := value(lookup, runtimeSettingsEncryptionEnvSelector); ok {
		return LoadEnvWithRuntimeSettingsEncryptionEnv(lookup, configured)
	}
	return loadEnv(lookup, defaultRuntimeSettingsEncryptionEnv, false)
}

// LoadEnvWithRuntimeSettingsEncryptionEnv loads configuration using name as
// the environment variable that contains the base64 runtime-settings key.
func LoadEnvWithRuntimeSettingsEncryptionEnv(lookup func(string) (string, bool), name string) (pgbase.Options, error) {
	return loadEnv(lookup, name, true)
}

func loadEnv(lookup func(string) (string, bool), name string, requireRuntimeSettingsKey bool) (pgbase.Options, error) {
	if !environmentName.MatchString(name) {
		return pgbase.Options{}, fmt.Errorf("runtime settings encryption environment name is invalid")
	}
	databaseURL, ok := value(lookup, "PGBASE_DATABASE_URL")
	if !ok || databaseURL == "" {
		return pgbase.Options{}, fmt.Errorf("PGBASE_DATABASE_URL: is required")
	}

	encodedSecret, ok := value(lookup, "PGBASE_TOKEN_SECRET")
	if !ok || encodedSecret == "" {
		return pgbase.Options{}, fmt.Errorf("PGBASE_TOKEN_SECRET: is required")
	}
	tokenSecret, err := base64.StdEncoding.DecodeString(encodedSecret)
	if err != nil {
		return pgbase.Options{}, fmt.Errorf("PGBASE_TOKEN_SECRET: decode base64: %w", err)
	}
	if len(tokenSecret) < 32 {
		return pgbase.Options{}, fmt.Errorf("PGBASE_TOKEN_SECRET: must decode to at least 32 bytes")
	}

	opts := pgbase.Options{DatabaseURL: databaseURL, TokenSecret: tokenSecret}
	encoded, configured := value(lookup, name)
	if requireRuntimeSettingsKey && (!configured || encoded == "") {
		return pgbase.Options{}, fmt.Errorf("runtime settings encryption key is required")
	}
	if configured && encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("runtime settings encryption key: decode base64: %w", err)
		}
		if len(key) < 32 {
			return pgbase.Options{}, fmt.Errorf("runtime settings encryption key: must decode to at least 32 bytes")
		}
		opts.RuntimeSettingsEncryptionKey = key
	}
	if err := loadBackups(lookup, &opts.Backups); err != nil {
		return pgbase.Options{}, err
	}
	if err := loadFiles(lookup, &opts); err != nil {
		return pgbase.Options{}, err
	}
	if err := loadOAuth(lookup, &opts); err != nil {
		return pgbase.Options{}, err
	}
	if err := loadEmail(lookup, &opts); err != nil {
		return pgbase.Options{}, err
	}
	if err := loadJobs(lookup, &opts); err != nil {
		return pgbase.Options{}, err
	}
	javascript, err := LoadJavaScriptEnv(lookup)
	if err != nil {
		return pgbase.Options{}, err
	}
	opts.JavaScript = javascript
	if raw, ok := value(lookup, "PGBASE_HTTP_ADDRESS"); ok {
		opts.HTTPAddress = raw
	}
	if err := loadTLS(lookup, &opts); err != nil {
		return pgbase.Options{}, err
	}
	if raw, ok := value(lookup, "PGBASE_PUBLIC_DIR"); ok {
		opts.PublicDir = raw
	}
	if raw, ok := value(lookup, "PGBASE_PUBLIC_SPA_FALLBACK"); ok && raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_PUBLIC_SPA_FALLBACK: %w", err)
		}
		opts.PublicSPAFallback = parsed
	}
	if raw, ok := value(lookup, "PGBASE_SETUP_TOKEN"); ok {
		opts.SetupToken = raw
	}
	if raw, ok := value(lookup, "PGBASE_METRICS_TOKEN"); ok && raw != "" {
		if len(raw) < 32 || len(raw) > 256 {
			return pgbase.Options{}, fmt.Errorf("PGBASE_METRICS_TOKEN: must contain between 32 and 256 bytes")
		}
		for _, character := range []byte(raw) {
			switch {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '-', character == '_', character == '.', character == '~':
			default:
				return pgbase.Options{}, fmt.Errorf("PGBASE_METRICS_TOKEN: must use bearer-token-safe characters")
			}
		}
		opts.MetricsToken = []byte(raw)
	}
	if raw, ok := value(lookup, "PGBASE_PUBLIC_URL"); ok && raw != "" {
		origin, err := parseOrigin(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_PUBLIC_URL: %w", err)
		}
		opts.PublicURL = origin
	}
	if raw, ok := value(lookup, "PGBASE_ALLOW_INSECURE_DATABASE"); ok && raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_ALLOW_INSECURE_DATABASE: %w", err)
		}
		opts.AllowInsecureDatabase = parsed
	}
	if raw, ok := value(lookup, "PGBASE_USER_REGISTRATION"); ok && raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_USER_REGISTRATION: %w", err)
		}
		opts.UserRegistrationEnabled = parsed
	}
	if raw, ok := value(lookup, "PGBASE_AUTO_MIGRATE"); ok && raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_AUTO_MIGRATE: %w", err)
		}
		opts.AutoMigrate = parsed
	}
	if raw, ok := value(lookup, "PGBASE_USER_MIGRATION_AUTO_RECORD_DIR"); ok {
		opts.UserMigrationAutoRecordDirectory = raw
	}
	if raw, ok := value(lookup, "PGBASE_TRUSTED_PROXIES"); ok && raw != "" {
		prefixes, err := parsePrefixes(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_TRUSTED_PROXIES: %w", err)
		}
		opts.TrustedProxies = prefixes
	}
	if raw, ok := value(lookup, "PGBASE_CORS_ALLOWED_ORIGINS"); ok && raw != "" {
		origins, err := parseOrigins(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_CORS_ALLOWED_ORIGINS: %w", err)
		}
		opts.CORSAllowedOrigins = origins
	}
	if err := parseDuration(lookup, "PGBASE_ADMIN_SESSION_TTL", &opts.AdminSessionTTL); err != nil {
		return pgbase.Options{}, err
	}
	if err := parseDuration(lookup, "PGBASE_STATEMENT_TIMEOUT", &opts.StatementTimeout); err != nil {
		return pgbase.Options{}, err
	}
	if err := parseDuration(lookup, "PGBASE_SHUTDOWN_TIMEOUT", &opts.ShutdownTimeout); err != nil {
		return pgbase.Options{}, err
	}
	if err := parseDuration(lookup, "PGBASE_SCHEMA_APPLY_TIMEOUT", &opts.SchemaApplyTimeout); err != nil {
		return pgbase.Options{}, err
	}
	var stopGracePeriod time.Duration
	stopGraceConfigured := false
	if raw, ok := value(lookup, "PGBASE_STOP_GRACE_PERIOD"); ok && raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return pgbase.Options{}, fmt.Errorf("PGBASE_STOP_GRACE_PERIOD: %w", err)
		}
		stopGracePeriod = parsed
		stopGraceConfigured = true
	}

	normalized, err := opts.Normalized()
	if err != nil {
		return pgbase.Options{}, fmt.Errorf("invalid pgBase configuration: %w", err)
	}
	if stopGraceConfigured && (stopGracePeriod < normalized.ShutdownTimeout || stopGracePeriod-normalized.ShutdownTimeout < 5*time.Second) {
		return pgbase.Options{}, fmt.Errorf("PGBASE_STOP_GRACE_PERIOD: must be at least PGBASE_SHUTDOWN_TIMEOUT plus 5s")
	}
	return normalized, nil
}

func value(lookup func(string) (string, bool), key string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	return lookup(key)
}

func parseDuration(lookup func(string) (string, bool), key string, target *time.Duration) error {
	raw, ok := value(lookup, key)
	if !ok || raw == "" {
		return nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*target = duration
	return nil
}

func parsePrefixes(raw string) ([]netip.Prefix, error) {
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || prefix != prefix.Masked() || prefix.Bits() == 0 {
			return nil, fmt.Errorf("invalid CIDR prefix %q", part)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseOrigins(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin, err := parseOrigin(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		origins = append(origins, origin.String())
	}
	return origins, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	if strings.Contains(raw, "#") {
		return nil, fmt.Errorf("must not include a path, query, or fragment")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, fmt.Errorf("must be an origin")
	}
	if parsed.Path != "" || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("must not include a path, query, or fragment")
	}
	if strings.Contains(parsed.Host, "*") {
		return nil, fmt.Errorf("must not contain a wildcard")
	}
	return parsed, nil
}
