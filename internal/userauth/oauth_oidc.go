package userauth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// verifyOIDCIDToken implements the constrained OIDC id_token fallback used
// when a provider intentionally has no userinfo endpoint. Only RS256 is
// accepted: accepting an algorithm selected by the token is a common JWT
// verification vulnerability. The JWKS and issuer URLs passed validation when
// the provider was configured, so this request inherits the hardened client.
func (p *HTTPOAuthProvider) verifyOIDCIDToken(ctx context.Context, raw, verifier string) (map[string]json.RawMessage, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 16384 {
		return nil, errors.New("OIDC id token is invalid")
	}
	decode := func(v string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(v) }
	headRaw, err := decode(parts[0])
	if err != nil {
		return nil, errors.New("OIDC id token is invalid")
	}
	var head struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if json.Unmarshal(headRaw, &head) != nil || head.Algorithm != "RS256" || head.KeyID == "" {
		return nil, errors.New("OIDC id token is invalid")
	}
	claimsRaw, err := decode(parts[1])
	if err != nil {
		return nil, errors.New("OIDC id token is invalid")
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(claimsRaw, &claims) != nil {
		return nil, errors.New("OIDC id token is invalid")
	}
	key, err := p.oidcRSAKey(ctx, head.KeyID)
	if err != nil {
		return nil, err
	}
	sig, err := decode(parts[2])
	if err != nil {
		return nil, errors.New("OIDC id token is invalid")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return nil, errors.New("OIDC id token signature is invalid")
	}
	var issuer, nonce string
	var exp float64
	if json.Unmarshal(claims["iss"], &issuer) != nil || issuer != p.config.Issuer || json.Unmarshal(claims["exp"], &exp) != nil || time.Now().Unix() >= int64(exp) || !oidcAudience(claims["aud"], p.config.ClientID) || json.Unmarshal(claims["nonce"], &nonce) != nil {
		return nil, errors.New("OIDC id token claims are invalid")
	}
	d := sha256.Sum256([]byte(verifier))
	if nonce != base64.RawURLEncoding.EncodeToString(d[:]) {
		return nil, errors.New("OIDC id token nonce is invalid")
	}
	return claims, nil
}

func oidcAudience(raw json.RawMessage, clientID string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, v := range many {
		if v == clientID {
			return true
		}
	}
	return false
}
func (p *HTTPOAuthProvider) oidcRSAKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			KeyID string `json:"kid"`
			Type  string `json:"kty"`
			N     string `json:"n"`
			E     string `json:"e"`
		} `json:"keys"`
	}
	if err := p.doJSON(req, &set); err != nil {
		return nil, err
	}
	for _, k := range set.Keys {
		if k.KeyID != kid || k.Type != "RSA" {
			continue
		}
		n, nerr := base64.RawURLEncoding.DecodeString(k.N)
		e, eerr := base64.RawURLEncoding.DecodeString(k.E)
		if nerr != nil || eerr != nil || len(e) == 0 || len(e) > 4 {
			break
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent<<8 | int(b)
		}
		if exponent < 3 {
			break
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}, nil
	}
	return nil, errors.New("OIDC signing key is unavailable")
}
