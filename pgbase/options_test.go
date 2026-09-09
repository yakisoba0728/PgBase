package pgbase

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOptionsDefaults(t *testing.T) {
	secret := make([]byte, 32)
	opts, err := (Options{
		DatabaseURL: "postgres://postgres:postgres@localhost/pgbase",
		TokenSecret: secret,
	}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if opts.HTTPAddress != "127.0.0.1:8090" {
		t.Fatalf("HTTPAddress = %q", opts.HTTPAddress)
	}
	if opts.AdminSessionTTL != 12*time.Hour {
		t.Fatalf("AdminSessionTTL = %s", opts.AdminSessionTTL)
	}
	if opts.StatementTimeout != 5*time.Second {
		t.Fatalf("StatementTimeout = %s", opts.StatementTimeout)
	}
	if opts.ShutdownTimeout != 15*time.Second {
		t.Fatalf("ShutdownTimeout = %s", opts.ShutdownTimeout)
	}
	if opts.SchemaApplyTimeout != 10*time.Minute {
		t.Fatalf("SchemaApplyTimeout = %s", opts.SchemaApplyTimeout)
	}
	if opts.Logger == nil {
		t.Fatal("Logger is nil")
	}
	secret[0] = 1
	if opts.TokenSecret[0] != 0 {
		t.Fatal("Normalized retained caller-owned token secret storage")
	}
}

func TestOptionsRejectsRelativePublicDirectory(t *testing.T) {
	_, err := (Options{DatabaseURL: "postgres://postgres:postgres@localhost/pgbase", TokenSecret: make([]byte, 32), PublicDir: "public"}).Normalized()
	if err == nil || !strings.Contains(err.Error(), "PublicDir") {
		t.Fatalf("error=%v", err)
	}
}

func TestOptionsRejectFileSettingsWithoutDriver(t *testing.T) {
	_, err := (Options{
		DatabaseURL: "postgres://postgres:postgres@localhost/pgbase",
		TokenSecret: make([]byte, 32),
		Files:       FileOptions{MaximumBytes: 1024},
	}).Normalized()
	if err == nil || !strings.Contains(err.Error(), "Files.Driver") {
		t.Fatalf("Normalized error = %v, want Files.Driver", err)
	}
}

func TestOptionsRejectInvalidSecurityAndOriginConfiguration(t *testing.T) {
	validURL := "postgres://postgres:postgres@localhost/pgbase"
	validSecret := make([]byte, 32)
	publicURL, err := url.Parse("https://pgbase.example")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "database URL is required",
			opts: Options{TokenSecret: validSecret},
			want: "DatabaseURL",
		},
		{
			name: "database URL must be PostgreSQL",
			opts: Options{DatabaseURL: "https://example.com", TokenSecret: validSecret},
			want: "DatabaseURL",
		},
		{
			name: "remote database requires verified TLS",
			opts: Options{DatabaseURL: "postgres://postgres:postgres@db.example/pgbase?sslmode=require", TokenSecret: validSecret},
			want: "sslmode=verify-full",
		},
		{
			name: "remote database accepts verified TLS",
			opts: Options{DatabaseURL: "postgres://postgres:postgres@db.example/pgbase?sslmode=verify-full", TokenSecret: validSecret},
		},
		{
			name: "Unix socket database is trusted-local",
			opts: Options{DatabaseURL: "postgres:///pgbase?host=/var/run/postgresql&sslmode=disable&pool_max_conns=4", TokenSecret: validSecret},
		},
		{
			name: "database URL without an authority or query host is rejected",
			opts: Options{DatabaseURL: "postgres:///pgbase", TokenSecret: validSecret},
			want: "host",
		},
		{
			name: "empty query host is rejected",
			opts: Options{DatabaseURL: "postgres:///pgbase?host=&sslmode=disable", TokenSecret: validSecret},
			want: "host",
		},
		{
			name: "query host cannot hide a remote database behind loopback authority",
			opts: Options{DatabaseURL: "postgres://postgres:postgres@127.0.0.1/pgbase?host=db.example&sslmode=disable", TokenSecret: validSecret},
			want: "sslmode=verify-full",
		},
		{
			name: "every query host must be trusted-local without verified TLS",
			opts: Options{DatabaseURL: "postgres:///pgbase?host=/var/run/postgresql,db.example&sslmode=disable", TokenSecret: validSecret},
			want: "sslmode=verify-full",
		},
		{
			name: "explicit development policy accepts insecure database transport",
			opts: Options{DatabaseURL: "postgres://postgres:postgres@postgres/pgbase?sslmode=disable", AllowInsecureDatabase: true, TokenSecret: validSecret},
		},
		{
			name: "token secret requires 32 bytes",
			opts: Options{DatabaseURL: validURL, TokenSecret: make([]byte, 31)},
			want: "TokenSecret",
		},
		{
			name: "setup token requires 32 characters",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, SetupToken: strings.Repeat("x", 31)},
			want: "SetupToken",
		},
		{
			name: "setup token must be valid UTF-8",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, SetupToken: strings.Repeat("\xff", 32)},
			want: "SetupToken",
		},
		{
			name: "setup token must fit the credential request budget",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, SetupToken: strings.Repeat("x", maximumSetupTokenBytes+1)},
			want: "SetupToken",
		},
		{
			name: "metrics token requires 32 bytes",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, MetricsToken: []byte(strings.Repeat("m", 31))},
			want: "MetricsToken",
		},
		{
			name: "metrics token rejects header separators",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, MetricsToken: []byte(strings.Repeat("m", 32) + " ")},
			want: "MetricsToken",
		},
		{
			name: "public URL cannot include a path",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, PublicURL: &url.URL{Scheme: "https", Host: "pgbase.example", Path: "/admin"}},
			want: "PublicURL",
		},
		{
			name: "public URL cannot include an empty query",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, PublicURL: &url.URL{Scheme: "https", Host: "pgbase.example", ForceQuery: true}},
			want: "PublicURL",
		},
		{
			name: "public HTTP URL must be loopback",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, PublicURL: &url.URL{Scheme: "http", Host: "pgbase.example"}},
			want: "PublicURL",
		},
		{
			name: "loopback public HTTP URL is accepted",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, PublicURL: &url.URL{Scheme: "http", Host: "[::1]:8090"}},
		},
		{
			name: "wildcard private backend with public HTTPS URL is accepted",
			opts: Options{DatabaseURL: validURL, HTTPAddress: ":8090", TokenSecret: validSecret, PublicURL: publicURL},
		},
		{
			name: "CORS origin cannot include a wildcard",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, CORSAllowedOrigins: []string{"https://*.example"}},
			want: "CORSAllowedOrigins",
		},
		{
			name: "trusted proxy must be a CIDR prefix",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, TrustedProxies: []netip.Prefix{{}}},
			want: "TrustedProxies",
		},
		{
			name: "trusted proxy cannot cover every IPv4 address",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
			want: "TrustedProxies",
		},
		{
			name: "trusted proxy cannot cover every IPv6 address",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("::/0")}},
			want: "TrustedProxies",
		},
		{
			name: "administrator session TTL cannot exceed twelve hours",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, AdminSessionTTL: 12*time.Hour + time.Nanosecond},
			want: "AdminSessionTTL",
		},
		{
			name: "statement timeout cannot exceed the PostgreSQL millisecond ceiling",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, StatementTimeout: maximumStatementTimeout + time.Nanosecond},
			want: "StatementTimeout",
		},
		{
			name: "statement timeout at the PostgreSQL millisecond ceiling is accepted",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, StatementTimeout: maximumStatementTimeout},
		},
		{
			name: "automatic migration directory rejects NUL",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, UserMigrationAutoRecordDirectory: "migrations\x00hidden"},
			want: "UserMigrationAutoRecordDirectory",
		},
		{
			name: "automatic migration directory has a bounded path",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, UserMigrationAutoRecordDirectory: strings.Repeat("m", 4097)},
			want: "UserMigrationAutoRecordDirectory",
		},
		{
			name: "valid public URL is accepted",
			opts: Options{DatabaseURL: validURL, TokenSecret: validSecret, PublicURL: publicURL, CORSAllowedOrigins: []string{"https://admin.example:8443"}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.opts.Normalized()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q validation error, got %v", tt.want, err)
			}
		})
	}
}

func TestOptionsCopiesMetricsToken(t *testing.T) {
	token := []byte(strings.Repeat("m", 32))
	opts, err := (Options{
		DatabaseURL:  "postgres://postgres:postgres@localhost/pgbase",
		TokenSecret:  make([]byte, 32),
		MetricsToken: token,
	}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	token[0] = 'x'
	if opts.MetricsToken[0] != 'm' {
		t.Fatal("Normalized retained caller-owned metrics token storage")
	}
}

func TestOptionsMalformedDatabaseURLDoesNotEchoCredentials(t *testing.T) {
	const secret = "options-database-secret-must-not-leak"
	_, err := (Options{
		DatabaseURL: "postgres://admin:" + secret + "%zz@localhost/pgbase",
		TokenSecret: make([]byte, 32),
	}).Normalized()
	if err == nil {
		t.Fatal("Normalized() error = nil, want malformed database URL rejection")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("database URL validation error leaked connection details: %q", err)
	}
}

func TestOptionsRejectsSetupTokenWithFewerThan32UnicodeCharacters(t *testing.T) {
	_, err := (Options{
		DatabaseURL: "postgres://postgres:postgres@localhost/pgbase",
		TokenSecret: make([]byte, 32),
		SetupToken:  strings.Repeat("😀", 8),
	}).Normalized()
	if err == nil || !strings.Contains(err.Error(), "SetupToken") {
		t.Fatalf("expected setup-token validation error, got %v", err)
	}
}
