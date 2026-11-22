package userauth

import (
	"context"
	"errors"
	"testing"
)

func TestOAuthVerifierBindingFailsClosed(t *testing.T) {
	s := &Service{secret: []byte("0123456789abcdef0123456789abcdef")}
	ctx := context.Background()
	const verifier = "server-generated-verifier"
	unbound, err := s.sealOAuthVerifier(ctx, verifier)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.sealOAuthVerifier(WithOAuthVerifierBinding(ctx), verifier)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		ciphertext []byte
		presented  *string
		ok         bool
	}{
		{"unbound without verifier", unbound, nil, true},
		{"unbound with matching verifier", unbound, ptr(verifier), true},
		{"unbound with wrong verifier", unbound, ptr("wrong"), false},
		{"unbound with empty verifier", unbound, ptr(""), false},
		{"bound without verifier", bound, nil, false},
		{"bound with wrong verifier", bound, ptr("wrong"), false},
		{"bound with empty verifier", bound, ptr(""), false},
		{"bound with matching verifier", bound, ptr(verifier), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestCtx := ctx
			if test.presented != nil {
				requestCtx = WithOAuthCodeVerifier(ctx, *test.presented)
			}
			got, err := s.openOAuthVerifier(requestCtx, test.ciphertext)
			if test.ok {
				if err != nil || got != verifier {
					t.Fatalf("verifier=%q err=%v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidCredentials) || got != "" {
				t.Fatalf("verifier=%q err=%v, want invalid credentials", got, err)
			}
		})
	}
	if _, err := s.openOAuthVerifier(ctx, []byte("not a sealed verifier")); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("corrupt ciphertext = %v, want a non-credential error", err)
	}
}

func ptr(value string) *string { return &value }
