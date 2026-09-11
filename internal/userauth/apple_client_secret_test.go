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
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestGenerateAppleClientSecretSignsBoundES256JWT(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 2, 3, 4, 900, time.FixedZone("KST", 9*60*60))
	secret, err := generateAppleClientSecretAt(AppleClientSecretInput{ClientID: "com.example.web", TeamID: "ABCDEFGHIJ", KeyID: "KLMNOPQRST", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})), Duration: 3600}, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(secret, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d", len(parts))
	}
	var header map[string]any
	var claims map[string]any
	decodeAppleSecretPart(t, parts[0], &header)
	decodeAppleSecretPart(t, parts[1], &claims)
	if header["alg"] != "ES256" || header["kid"] != "KLMNOPQRST" || claims["iss"] != "ABCDEFGHIJ" || claims["sub"] != "com.example.web" || claims["aud"] != appleClientSecretAudience || claims["iat"] != float64(now.UTC().Truncate(time.Second).Unix()) || claims["exp"] != float64(now.UTC().Truncate(time.Second).Add(time.Hour).Unix()) {
		t.Fatalf("header=%v claims=%v", header, claims)
	}
	r, s, err := parseAppleClientSecretSignature(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Fatal("signature does not verify with the P-256 public key")
	}
}

func parseAppleClientSecretSignature(raw string) (*big.Int, *big.Int, error) {
	signature, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(signature) != 64 {
		return nil, nil, err
	}
	return new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:]), nil
}

func TestGenerateAppleClientSecretRejectsInvalidInputsWithoutEchoingKey(t *testing.T) {
	badKey := "-----BEGIN PRIVATE KEY-----\nprivate-key-must-not-escape\n-----END PRIVATE KEY-----"
	for _, input := range []AppleClientSecretInput{
		{ClientID: "", TeamID: "ABCDEFGHIJ", KeyID: "KLMNOPQRST", PrivateKey: badKey, Duration: 1},
		{ClientID: "id", TeamID: "short", KeyID: "KLMNOPQRST", PrivateKey: badKey, Duration: 1},
		{ClientID: "id", TeamID: "ABCDEFGHIJ", KeyID: "short", PrivateKey: badKey, Duration: 1},
		{ClientID: "id", TeamID: "ABCDEFGHIJ", KeyID: "KLMNOPQRST", PrivateKey: badKey, Duration: 0},
		{ClientID: "id", TeamID: "ABCDEFGHIJ", KeyID: "KLMNOPQRST", PrivateKey: badKey, Duration: maximumAppleSecretLifetime + 1},
	} {
		if _, err := GenerateAppleClientSecret(input); err == nil || strings.Contains(err.Error(), "private-key-must-not-escape") {
			t.Fatalf("invalid Apple client-secret input was accepted or exposed its key: %v", err)
		}
	}
}

func decodeAppleSecretPart(t *testing.T, raw string, destination any) {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(data, destination) != nil {
		t.Fatalf("invalid JWT JSON: %v", err)
	}
}
