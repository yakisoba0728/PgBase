package observability

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactRemovesSecrets(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{name: "URL password", input: "https://admin:secret@example.com/app", secret: "secret"},
		{name: "Authorization", input: "Authorization: Bearer bearer-secret", secret: "bearer-secret"},
		{name: "Cookie", input: "Cookie: session=abc", secret: "session=abc"},
		{name: "Set-Cookie", input: "Set-Cookie: sid=def", secret: "sid=def"},
		{name: "setup token", input: "setup token=setup-secret", secret: "setup-secret"},
		{name: "session token", input: "session_token=session-secret", secret: "session-secret"},
		{name: "generic secret", input: "client_secret=oauth-secret", secret: "oauth-secret"},
		{name: "API key", input: "api_key=provider-key", secret: "provider-key"},
		{name: "prefixed API key", input: "provider_api_key=provider-prefixed-key", secret: "provider-prefixed-key"},
		{name: "private key", input: "private-key=signing-key", secret: "signing-key"},
		{name: "colon secret", input: "client_secret:oauth-secret", secret: "oauth-secret"},
		{name: "colon quoted secret", input: "password: 'quoted secret' sslmode=disable", secret: "quoted secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if strings.Contains(got, tt.secret) {
				t.Fatalf("redaction leaked %q: %q", tt.secret, got)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("redaction = %q", got)
			}
		})
	}
}

func TestJSONLoggerRedactsTypedSecretAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSONLogger(&output)
	logger.Info("request", slog.String("authorization", "Bearer bearer-secret"), slog.String("database_url", "postgres://admin:secret@db.example/app"), slog.String("client_secret", "oauth-client-secret"), slog.String("provider_api_key", "provider-key"), slog.String("method", "GET"))
	if got := output.String(); strings.Contains(got, "bearer-secret") || strings.Contains(got, "admin:secret") || strings.Contains(got, "oauth-client-secret") || strings.Contains(got, "provider-key") {
		t.Fatalf("log leaked secret: %q", got)
	}
	if !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("log = %q", output.String())
	}
}

func TestJSONLoggerRedactsCredentialsInErrorAttributes(t *testing.T) {
	tests := []struct {
		name   string
		error  error
		secret string
		keep   string
	}{
		{
			name:   "PostgreSQL URL query password",
			error:  errors.New("connect postgres://db.example/app?password=query-secret&sslmode=disable failed"),
			secret: "query-secret",
			keep:   "sslmode=disable",
		},
		{
			name:   "keyword DSN password",
			error:  errors.New("connect host=db.example user=pgbase password=keyword-secret sslmode=disable failed"),
			secret: "keyword-secret",
			keep:   "user=pgbase",
		},
		{
			name:   "PostgreSQL URL sslpassword",
			error:  errors.New("connect postgres://db.example/app?sslpassword=tls-query-secret&sslmode=verify-full failed"),
			secret: "tls-query-secret",
			keep:   "sslmode=verify-full",
		},
		{
			name:   "keyword DSN sslpassword",
			error:  errors.New("connect host=db.example sslpassword='tls keyword secret' sslmode=verify-full failed"),
			secret: "tls keyword secret",
			keep:   "host=db.example",
		},
		{
			name:   "percent-encoded PostgreSQL URL sslpassword",
			error:  errors.New("cannot parse postgres://db.example/app?sslpass%77ord=encoded-query-secret&sslmode=invalid"),
			secret: "encoded-query-secret",
			keep:   "sslmode=invalid",
		},
		{
			name:   "mixed-case encoded PostgreSQL URL sslpassword",
			error:  errors.New("cannot parse postgres://db.example/app?sslpass%57ord=mixed-query-secret&sslmode=invalid"),
			secret: "mixed-query-secret",
			keep:   "sslmode=invalid",
		},
		{
			name:   "double-encoded PostgreSQL URL sslpassword",
			error:  errors.New("cannot parse postgres://db.example/app?sslpass%2577ord=double-query-secret&sslmode=invalid"),
			secret: "double-query-secret",
			keep:   "sslmode=invalid",
		},
		{
			name:   "raw space in PostgreSQL URL sslpassword",
			error:  errors.New("cannot parse postgres://db.example/app?sslpassword=prefix raw-space-secret-suffix&pool_max_conns=invalid"),
			secret: "raw-space-secret-suffix",
			keep:   "pool_max_conns=invalid",
		},
		{
			name:   "percent-encoded keyword DSN sslpassword",
			error:  errors.New("cannot parse host=db.example sslpass%77ord='encoded keyword secret' sslmode=invalid"),
			secret: "encoded keyword secret",
			keep:   "host=db.example",
		},
		{
			name:   "escaped quote in keyword DSN password",
			error:  errors.New(`cannot parse host=db.example password='prefix\'escaped-secret-suffix' sslmode=invalid`),
			secret: "escaped-secret-suffix",
			keep:   "host=db.example",
		},
		{
			name:   "escaped space in unquoted keyword DSN password",
			error:  errors.New(`cannot parse host=db.example password=prefix\ escaped-space-secret sslmode=invalid`),
			secret: "escaped-space-secret",
			keep:   "host=db.example",
		},
		{
			name:   "single-quoted URL query password with whitespace",
			error:  errors.New("cannot parse postgres://db.example/app?password='prefix secret-suffix'&sslmode=invalid"),
			secret: "secret-suffix",
			keep:   "sslmode=invalid",
		},
		{
			name:   "backtick URL query TLS password with whitespace",
			error:  errors.New("cannot parse postgres://db.example/app?sslpassword=`prefix secret-suffix`&sslmode=invalid"),
			secret: "secret-suffix",
			keep:   "sslmode=invalid",
		},
		{
			name:   "backtick keyword DSN password with whitespace",
			error:  errors.New("cannot parse host=db.example password=`prefix secret-suffix` sslmode=invalid"),
			secret: "secret-suffix",
			keep:   "host=db.example",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			NewJSONLogger(&output).Error("connect failed", "error", tt.error, "component", "database")
			got := output.String()
			if strings.Contains(got, tt.secret) {
				t.Fatalf("log leaked %q: %q", tt.secret, got)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("log = %q", got)
			}
			if !strings.Contains(got, `"component":"database"`) {
				t.Fatalf("log corrupted unrelated attribute: %q", got)
			}
			if !strings.Contains(got, tt.keep) {
				t.Fatalf("log corrupted unrelated error field %q: %q", tt.keep, got)
			}
		})
	}
}

func TestRedactPreservesNonSecretParameters(t *testing.T) {
	const input = "connect postgres://db.example/app?sslmode=verify-full&application_name=pgbase failed"
	if got := Redact(input); got != input {
		t.Fatalf("Redact() = %q, want unchanged %q", got, input)
	}
}
