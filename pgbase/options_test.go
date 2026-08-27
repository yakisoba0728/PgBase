package pgbase

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOptionsDefaults(t *testing.T) {
	opts, err := (Options{
		DatabaseURL: "postgres://postgres:postgres@localhost/pgbase",
		TokenSecret: make([]byte, 32),
	}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if opts.HTTPAddress != ":8090" {
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
	if opts.Logger == nil {
		t.Fatal("Logger is nil")
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
