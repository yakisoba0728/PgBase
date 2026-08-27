package adminauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestTokenGenerationProduces256BitsOfURLSafeMaterial(t *testing.T) {
	first, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode generated token: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded token length = %d, want 32", len(decoded))
	}
	if first == second {
		t.Fatal("two generated tokens were equal")
	}
}

func TestTokenDomainsSeparateSessionAndCSRFMaterial(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	raw := "fixed-session-token"

	wantSessionMAC := hmac.New(sha256.New, secret)
	wantSessionMAC.Write([]byte("session\x00" + raw))
	wantSession := wantSessionMAC.Sum(nil)
	gotSession := HashSessionToken(secret, raw)
	if !hmac.Equal(gotSession, wantSession) {
		t.Fatalf("session HMAC = %x, want %x", gotSession, wantSession)
	}

	wantCSRFMAC := hmac.New(sha256.New, secret)
	wantCSRFMAC.Write([]byte("csrf\x00" + raw))
	wantCSRF := base64.RawURLEncoding.EncodeToString(wantCSRFMAC.Sum(nil))
	if got := DeriveCSRFToken(secret, raw); got != wantCSRF {
		t.Fatalf("CSRF token = %q, want %q", got, wantCSRF)
	}
	if base64.RawURLEncoding.EncodeToString(gotSession) == wantCSRF {
		t.Fatal("session and CSRF derivations collided")
	}
}
