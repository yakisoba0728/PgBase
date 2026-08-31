package pgbase

import (
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumAdminSessionTTL = 12 * time.Hour

// Options configures a pgBase application.
type Options struct {
	DatabaseURL string
	HTTPAddress string
	PublicURL   *url.URL
	SetupToken  string
	TokenSecret []byte
	// MetricsToken enables the protected Prometheus-compatible GET /metrics
	// endpoint when non-empty. It is copied during normalization so callers
	// cannot change the live credential after constructing an App.
	MetricsToken       []byte
	AutoMigrate        bool
	TrustedProxies     []netip.Prefix
	CORSAllowedOrigins []string
	AdminSessionTTL    time.Duration
	StatementTimeout   time.Duration
	ShutdownTimeout    time.Duration
	Logger             *slog.Logger
}

// Normalized applies default values and validates option values.
func (o Options) Normalized() (Options, error) {
	if o.HTTPAddress == "" {
		o.HTTPAddress = ":8090"
	}
	if o.AdminSessionTTL == 0 {
		o.AdminSessionTTL = 12 * time.Hour
	}
	if o.StatementTimeout == 0 {
		o.StatementTimeout = 5 * time.Second
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = 15 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if err := validateOptions(o); err != nil {
		return Options{}, err
	}
	o.TokenSecret = append([]byte(nil), o.TokenSecret...)
	o.MetricsToken = append([]byte(nil), o.MetricsToken...)
	return o, nil
}

func validateOptions(o Options) error {
	if err := validateDatabaseURL(o.DatabaseURL); err != nil {
		return fmt.Errorf("DatabaseURL: %w", err)
	}
	if len(o.TokenSecret) < 32 {
		return fmt.Errorf("TokenSecret: must contain at least 32 bytes")
	}
	if len(o.MetricsToken) != 0 {
		if len(o.MetricsToken) < 32 || len(o.MetricsToken) > 256 {
			return fmt.Errorf("MetricsToken: must contain between 32 and 256 bytes")
		}
		for _, character := range o.MetricsToken {
			switch {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '-', character == '_', character == '.', character == '~':
			default:
				return fmt.Errorf("MetricsToken: must use bearer-token-safe characters")
			}
		}
	}
	if o.SetupToken != "" && utf8.RuneCountInString(o.SetupToken) < 32 {
		return fmt.Errorf("SetupToken: must contain at least 32 characters")
	}
	if o.PublicURL != nil {
		if err := validateOrigin(o.PublicURL); err != nil {
			return fmt.Errorf("PublicURL: %w", err)
		}
	}
	for _, origin := range o.CORSAllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil {
			return fmt.Errorf("CORSAllowedOrigins: %w", err)
		}
		if err := validateOrigin(parsed); err != nil {
			return fmt.Errorf("CORSAllowedOrigins: %w", err)
		}
	}
	for _, prefix := range o.TrustedProxies {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			return fmt.Errorf("TrustedProxies: must contain valid CIDR prefixes")
		}
	}
	if o.AdminSessionTTL <= 0 || o.AdminSessionTTL > maximumAdminSessionTTL {
		return fmt.Errorf("AdminSessionTTL: must be positive and no greater than 12h")
	}
	if o.StatementTimeout <= 0 {
		return fmt.Errorf("StatementTimeout: must be positive")
	}
	if o.ShutdownTimeout <= 0 {
		return fmt.Errorf("ShutdownTimeout: must be positive")
	}
	return nil
}

func validateDatabaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("must use postgres or postgresql scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateOrigin(origin *url.URL) error {
	if origin == nil {
		return fmt.Errorf("is required")
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if origin.Host == "" || origin.User != nil || origin.Opaque != "" {
		return fmt.Errorf("must be an origin")
	}
	if origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("must not include a path, query, or fragment")
	}
	if strings.Contains(origin.Host, "*") {
		return fmt.Errorf("must not contain a wildcard")
	}
	return nil
}
