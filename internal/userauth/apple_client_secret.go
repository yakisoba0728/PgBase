package userauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	appleClientSecretAudience  = "https://appleid.apple.com"
	maximumAppleSecretLifetime = 15777000 // Apple permits up to roughly six months.
)

var appleIdentifier = regexp.MustCompile(`^[A-Za-z0-9]{10}$`)

// AppleClientSecretInput contains the one-time values Apple requires to sign a
// client secret. PrivateKey is intentionally never persisted or logged.
type AppleClientSecretInput struct {
	ClientID   string
	TeamID     string
	KeyID      string
	PrivateKey string
	Duration   int
}

// GenerateAppleClientSecret creates a short-lived ES256 JWT suitable for the
// client_secret parameter of Apple's token endpoint.
func GenerateAppleClientSecret(input AppleClientSecretInput) (string, error) {
	return generateAppleClientSecretAt(input, time.Now())
}

func generateAppleClientSecretAt(input AppleClientSecretInput, now time.Time) (string, error) {
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.TeamID = strings.TrimSpace(input.TeamID)
	input.KeyID = strings.TrimSpace(input.KeyID)
	if input.ClientID == "" || len(input.ClientID) > 255 || !appleIdentifier.MatchString(input.TeamID) || !appleIdentifier.MatchString(input.KeyID) || input.Duration < 1 || input.Duration > maximumAppleSecretLifetime {
		return "", ErrValidation
	}
	key, err := parseAppleP8Key(input.PrivateKey)
	if err != nil {
		return "", ErrValidation
	}
	issuedAt := now.UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(time.Duration(input.Duration) * time.Second)
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": input.KeyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": input.TeamID,
		"sub": input.ClientID,
		"aud": appleClientSecretAudience,
		"iat": issuedAt.Unix(),
		"exp": expiresAt.Unix(),
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", errors.New("sign Apple client secret")
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseAppleP8Key(raw string) (*ecdsa.PrivateKey, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("Apple private key is not PKCS#8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("Apple private key is invalid")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() || key.D == nil || key.D.Sign() <= 0 || key.D.Cmp(key.Curve.Params().N) >= 0 || key.X == nil || key.Y == nil || !key.Curve.IsOnCurve(key.X, key.Y) {
		return nil, errors.New("Apple private key must use P-256")
	}
	return key, nil
}
