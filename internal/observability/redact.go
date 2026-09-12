// Package observability provides safe structured logging helpers.
package observability

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	urlPassword         = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:\s/@]+:)[^@\s/]+@`)
	urlQueryParameter   = regexp.MustCompile("([?&])([^=&#\\s]+)=([^&\\r\\n]*)")
	keywordDSNParameter = regexp.MustCompile("(\\b[^\\s:=&?]+)(\\s*[:=]\\s*)('(?:\\\\.|[^'])*'|\"(?:\\\\.|[^\"])*\"|`(?:\\\\.|[^`])*`|(?:\\\\.|[^\\s&])+)")
	headerSecret        = regexp.MustCompile(`(?i)((?:authorization|cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	bearerSecret        = regexp.MustCompile(`(?i)(\bbearer\s+)[^\s,;]+`)
	tokenSecret         = regexp.MustCompile(`(?i)((?:setup|session)[ _-]?token\s*[:=]\s*)[^\s,;]+`)
)

// Redact removes values that must not be written to process logs.
func Redact(value string) string {
	value = urlPassword.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = redactSecretParameters(value, urlQueryParameter, 2, 3)
	value = redactSecretParameters(value, keywordDSNParameter, 1, 3)
	value = headerSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	value = bearerSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	return tokenSecret.ReplaceAllString(value, `${1}[REDACTED]`)
}

func redactSecretParameters(value string, pattern *regexp.Regexp, keyGroup, valueGroup int) string {
	return pattern.ReplaceAllStringFunc(value, func(match string) string {
		indexes := pattern.FindStringSubmatchIndex(match)
		keyStart, keyEnd := indexes[keyGroup*2], indexes[keyGroup*2+1]
		valueStart := indexes[valueGroup*2]
		if keyStart < 0 || keyEnd < 0 || valueStart < 0 || !sensitiveParameterKey(match[keyStart:keyEnd]) {
			return match
		}
		return match[:valueStart] + "[REDACTED]"
	})
}

func sensitiveParameterKey(key string) bool {
	for range 3 {
		decoded, err := url.QueryUnescape(key)
		if err != nil || decoded == key {
			break
		}
		key = decoded
	}
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return strings.Contains(key, "password") || strings.Contains(key, "token") ||
		strings.Contains(key, "secret") || key == "pass" || strings.Contains(key, "api_key") ||
		strings.Contains(key, "apikey") || strings.Contains(key, "private_key")
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return strings.Contains(key, "password") || strings.Contains(key, "authorization") ||
		strings.Contains(key, "cookie") || strings.Contains(key, "token") ||
		strings.Contains(key, "secret") || strings.Contains(key, "api_key") || strings.Contains(key, "apikey") ||
		strings.Contains(key, "private_key") || strings.Contains(key, "database_url") || strings.Contains(key, "dsn")
}
