package userauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
)

// The PKCE verifier of every OAuth flow is generated and kept by the server,
// which presents it in the provider token exchange. A client that opts in at
// start (or link) additionally binds completion to itself: the flow is sealed
// under a separate encryption domain, so the binding is authenticated by the
// same AEAD as the verifier, and completion then fails closed unless the
// client presents the verifier it received from start. A stolen callback
// (state and code) alone can then no longer finish that flow.
const (
	oauthVerifierDomain      = "oauth-verifier"
	oauthBoundVerifierDomain = "oauth-verifier-bound"
)

type oauthVerifierBindingKey struct{}
type oauthCodeVerifierKey struct{}

// WithOAuthVerifierBinding makes the OAuth start or link begun with this
// context require the returned codeVerifier at completion.
func WithOAuthVerifierBinding(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauthVerifierBindingKey{}, true)
}

// WithOAuthCodeVerifier attaches the PKCE verifier a client presents at OAuth
// completion. Once attached it must match, so an empty value fails closed.
func WithOAuthCodeVerifier(ctx context.Context, verifier string) context.Context {
	return context.WithValue(ctx, oauthCodeVerifierKey{}, verifier)
}

func (s *Service) sealOAuthVerifier(ctx context.Context, verifier string) ([]byte, error) {
	domain := oauthVerifierDomain
	if bound, _ := ctx.Value(oauthVerifierBindingKey{}).(bool); bound {
		domain = oauthBoundVerifierDomain
	}
	return s.encrypt(domain, []byte(verifier))
}

// openOAuthVerifier returns the stored verifier for the provider exchange. A
// presented verifier must always match; a bound flow also requires one.
func (s *Service) openOAuthVerifier(ctx context.Context, ciphertext []byte) (string, error) {
	bound := false
	plain, err := s.decrypt(oauthVerifierDomain, ciphertext)
	if err != nil {
		if plain, err = s.decrypt(oauthBoundVerifierDomain, ciphertext); err != nil {
			return "", err
		}
		bound = true
	}
	presented, ok := ctx.Value(oauthCodeVerifierKey{}).(string)
	if !ok {
		if bound {
			return "", ErrInvalidCredentials
		}
		return string(plain), nil
	}
	want, got := sha256.Sum256(plain), sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		return "", ErrInvalidCredentials
	}
	return string(plain), nil
}
