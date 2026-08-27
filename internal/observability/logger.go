package observability

import (
	"io"
	"log/slog"
)

// NewJSONLogger returns a JSON logger which redacts secrets from messages and
// typed attributes. Callers should log individual request fields, not request
// or response structs that can contain credentials and cookies.
func NewJSONLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.MessageKey && attr.Value.Kind() == slog.KindString {
				return slog.String(attr.Key, Redact(attr.Value.String()))
			}
			if sensitiveKey(attr.Key) {
				return slog.String(attr.Key, "[REDACTED]")
			}
			if attr.Value.Kind() == slog.KindString {
				return slog.String(attr.Key, Redact(attr.Value.String()))
			}
			if attr.Value.Kind() == slog.KindAny {
				if err, ok := attr.Value.Any().(error); ok {
					return slog.String(attr.Key, Redact(err.Error()))
				}
			}
			return attr
		},
	}))
}
