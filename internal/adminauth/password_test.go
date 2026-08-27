package adminauth

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashRoundTripUsesRequiredArgon2idEncoding(t *testing.T) {
	passwords := NewArgon2idPasswords()
	encoded, err := passwords.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("encoded hash has unexpected parameters: %q", encoded)
	}
	if err := passwords.Verify(encoded, "correct horse battery staple"); err != nil {
		t.Fatalf("verify matching password: %v", err)
	}
	if err := passwords.Verify(encoded, "incorrect horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("verify wrong password error = %v, want ErrInvalidCredentials", err)
	}
}

func TestPasswordVerifyRejectsMalformedHashesAsInvalidCredentials(t *testing.T) {
	passwords := NewArgon2idPasswords()
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=4$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
		"$argon2id$v=19$m=65536,t=2,p=4$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
	}
	for _, encoded := range cases {
		if err := passwords.Verify(encoded, "correct horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Verify(%q) error = %v, want ErrInvalidCredentials", encoded, err)
		}
	}
}

func TestPasswordHashValidatesUTF8AndByteLength(t *testing.T) {
	passwords := NewArgon2idPasswords()
	cases := []string{
		"too-short",
		strings.Repeat("a", 1025),
		string([]byte{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf9, 0xf8, 0xf7, 0xf6, 0xf5, 0xf4}),
	}
	for _, password := range cases {
		if _, err := passwords.Hash(password); !errors.Is(err, ErrValidation) {
			t.Errorf("Hash invalid password error = %v, want ErrValidation", err)
		}
	}
	for _, password := range []string{strings.Repeat("a", 12), strings.Repeat("a", 1024), strings.Repeat("가", 4)} {
		if _, err := passwords.Hash(password); err != nil {
			t.Errorf("Hash valid %d-byte password: %v", len(password), err)
		}
	}
}

func TestNormalizeEmailRequiresCanonicalBareASCIIAddress(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: " \tADMIN@Example.COM\n", want: "admin@example.com", ok: true},
		{input: "\u3000ADMIN@Example.COM\u3000", want: "admin@example.com", ok: true},
		{input: "Admin <admin@example.com>", ok: false},
		{input: "admin@example.com,other@example.com", ok: false},
		{input: "admin@éxample.com", ok: false},
		{input: strings.Repeat("a", 243) + "@example.com", ok: false},
	}
	for _, tc := range cases {
		got, err := NormalizeEmail(tc.input)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q, nil", tc.input, got, err, tc.want)
		}
		if !tc.ok && !errors.Is(err, ErrValidation) {
			t.Errorf("NormalizeEmail(%q) error = %v, want ErrValidation", tc.input, err)
		}
	}
}
