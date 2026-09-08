package userauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubPasswords struct{}

func (stubPasswords) Hash(string) (string, error) { return "hash", nil }
func (stubPasswords) Verify(string, string) error { return nil }

func TestRegisterRejectsInvalidInputsBeforeDatabaseWork(t *testing.T) {
	s := NewService(nil, stubPasswords{}, []byte(strings.Repeat("s", 32)), time.Hour)
	for _, tc := range []struct{ name, email, password, address string }{
		{"email", "not an email", "correct horse battery", "client"},
		{"short password", "user@example.com", "too-short", "client"},
		{"oversized password", "user@example.com", strings.Repeat("x", maximumPasswordBytes+1), "client"},
		{"address", "user@example.com", "correct horse battery", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Register(context.Background(), tc.email, tc.password, tc.address)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("Register error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := normalizeEmail(" User@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "user@example.com" {
		t.Fatalf("normalized email = %q", got)
	}
	for _, v := range []string{"", "Name <user@example.com>", "ü@example.com", strings.Repeat("a", 255)} {
		if _, err := normalizeEmail(v); !errors.Is(err, ErrValidation) {
			t.Errorf("normalizeEmail(%q) error = %v", v, err)
		}
	}
}

func TestServiceCopiesSecretAndHashesTokensByDomain(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	s := NewService(nil, stubPasswords{}, secret, time.Hour)
	before := s.tokenHash("token")
	secret[0] = 'b'
	after := s.tokenHash("token")
	if !hmacEqual(before, after) {
		t.Fatal("service retained caller-owned secret")
	}
	if hmacEqual(s.tokenHash("token"), s.subjectHash("login", "network", "token")) {
		t.Fatal("token and limiter hashes share a domain")
	}
}

func TestValidateTokenRequiresCanonical256BitToken(t *testing.T) {
	valid := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err := validateToken(valid); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	for _, v := range []string{"", "raw-token", valid + "="} {
		if err := validateToken(v); !errors.Is(err, ErrValidation) {
			t.Errorf("validateToken(%q)=%v", v, err)
		}
	}
}

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
