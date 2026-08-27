package observability

import (
	"bytes"
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
	logger.Info("request", slog.String("authorization", "Bearer bearer-secret"), slog.String("database_url", "postgres://admin:secret@db.example/app"), slog.String("method", "GET"))
	if got := output.String(); strings.Contains(got, "bearer-secret") || strings.Contains(got, "admin:secret") {
		t.Fatalf("log leaked secret: %q", got)
	}
	if !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("log = %q", output.String())
	}
}
