// Package observability provides safe structured logging helpers.
package observability

import (
	"regexp"
	"strings"
)

var (
	urlPassword  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:\s/@]+:)[^@\s/]+@`)
	headerSecret = regexp.MustCompile(`(?i)((?:authorization|cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	tokenSecret  = regexp.MustCompile(`(?i)((?:setup|session)[ _-]?token\s*[:=]\s*)[^\s,;]+`)
)

// Redact removes values that must not be written to process logs.
func Redact(value string) string {
	value = urlPassword.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = headerSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	return tokenSecret.ReplaceAllString(value, `${1}[REDACTED]`)
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return strings.Contains(key, "password") || strings.Contains(key, "authorization") ||
		strings.Contains(key, "cookie") || strings.Contains(key, "token") ||
		strings.Contains(key, "database_url") || strings.Contains(key, "dsn")
}
