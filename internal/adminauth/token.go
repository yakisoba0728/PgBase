package adminauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const tokenBytes = 32

// GenerateToken returns 256 bits of cryptographically random URL-safe material.
func GenerateToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashSessionToken derives the value persisted for a raw session token.
func HashSessionToken(secret []byte, rawToken string) []byte {
	return tokenHMAC(secret, "session\x00", rawToken)
}

// DeriveCSRFToken derives the URL-safe CSRF token bound to a raw session token.
func DeriveCSRFToken(secret []byte, rawToken string) string {
	return base64.RawURLEncoding.EncodeToString(tokenHMAC(secret, "csrf\x00", rawToken))
}

func rateLimitSubjectHash(secret []byte, normalizedEmail, subject string) []byte {
	return tokenHMAC(secret, "rate-limit\x00", normalizedEmail+"\x00"+subject)
}

func tokenHMAC(secret []byte, domain, value string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
