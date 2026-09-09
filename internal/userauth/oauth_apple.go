package userauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const maximumAppleKeys = 16

type appleKeyCache struct {
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
}

func (p *HTTPOAuthProvider) exchangeAppleIdentity(ctx context.Context, rawToken, verifier string) (OAuthIdentity, error) {
	claims, err := p.verifyAppleIDToken(ctx, rawToken)
	if err != nil {
		return OAuthIdentity{}, err
	}
	digest := sha256.Sum256([]byte(verifier))
	expectedNonce := base64.RawURLEncoding.EncodeToString(digest[:])
	if scalarString(claims["nonce"]) != expectedNonce {
		return OAuthIdentity{}, errors.New("Apple nonce is invalid")
	}
	identity := OAuthIdentity{Subject: scalarString(claims["sub"])}
	if boolValue(claims["email_verified"]) {
		identity.Email = scalarString(claims["email"])
		identity.EmailVerified = identity.Email != ""
	}
	if identity.Subject == "" {
		return OAuthIdentity{}, errors.New("Apple subject is invalid")
	}
	return identity, nil
}

func (p *HTTPOAuthProvider) verifyAppleIDToken(ctx context.Context, raw string) (map[string]any, error) {
	parts := splitJWT(raw)
	if len(parts) != 3 {
		return nil, errors.New("Apple id_token is malformed")
	}
	var header map[string]any
	var claims map[string]any
	if decodeJWTPart(parts[0], &header) != nil || decodeJWTPart(parts[1], &claims) != nil {
		return nil, errors.New("Apple id_token is malformed")
	}
	if scalarString(header["alg"]) != "RS256" || scalarString(header["kid"]) == "" {
		return nil, errors.New("Apple id_token algorithm is invalid")
	}
	key, err := p.applePublicKey(ctx, scalarString(header["kid"]), false)
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("Apple id_token signature is malformed")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		key, err = p.applePublicKey(ctx, scalarString(header["kid"]), true)
		if err != nil || rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
			return nil, errors.New("Apple id_token signature is invalid")
		}
	}
	now := time.Now()
	issuer, err := appleIssuer(p.config.AuthorizationURL)
	if err != nil || scalarString(claims["iss"]) != issuer {
		return nil, errors.New("Apple issuer is invalid")
	}
	if !jwtAudienceContains(claims["aud"], p.config.ClientID) {
		return nil, errors.New("Apple audience is invalid")
	}
	exp, expOK := numericDate(claims["exp"])
	iat, iatOK := numericDate(claims["iat"])
	if !expOK || !iatOK || !now.Before(exp) || iat.After(now.Add(time.Minute)) {
		return nil, errors.New("Apple id_token time claims are invalid")
	}
	if nbf, ok := numericDate(claims["nbf"]); ok && now.Add(time.Minute).Before(nbf) {
		return nil, errors.New("Apple id_token is not active")
	}
	return claims, nil
}

func (p *HTTPOAuthProvider) applePublicKey(ctx context.Context, kid string, refresh bool) (*rsa.PublicKey, error) {
	p.apple.mu.Lock()
	defer p.apple.mu.Unlock()
	if !refresh && time.Now().Before(p.apple.expires) {
		if key := p.apple.keys[kid]; key != nil {
			return key, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct{ Kty, Kid, Use, Alg, N, E string } `json:"keys"`
	}
	if err := p.doJSON(req, &set); err != nil {
		return nil, err
	}
	if len(set.Keys) == 0 || len(set.Keys) > maximumAppleKeys {
		return nil, errors.New("Apple JWKS is invalid")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.Kty != "RSA" || jwk.Kid == "" || jwk.Alg != "" && jwk.Alg != "RS256" || jwk.Use != "" && jwk.Use != "sig" {
			continue
		}
		n, nerr := base64.RawURLEncoding.DecodeString(jwk.N)
		ebytes, eerr := base64.RawURLEncoding.DecodeString(jwk.E)
		if nerr != nil || eerr != nil || len(n) < 256 || len(ebytes) == 0 || len(ebytes) > 4 {
			continue
		}
		e := 0
		for _, b := range ebytes {
			e = e<<8 | int(b)
		}
		if e < 3 {
			continue
		}
		keys[jwk.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}
	}
	p.apple.keys, p.apple.expires = keys, time.Now().Add(time.Hour)
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("Apple signing key is unknown")
}

func splitJWT(raw string) []string {
	if len(raw) == 0 || len(raw) > 16384 {
		return nil
	}
	parts := []string{}
	start := 0
	for i := range raw {
		if raw[i] == '.' {
			parts = append(parts, raw[start:i])
			start = i + 1
		}
	}
	return append(parts, raw[start:])
}
func decodeJWTPart(raw string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(out)
}
func appleIssuer(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", err
	}
	return u.Scheme + "://" + u.Host, nil
}
func jwtAudienceContains(value any, audience string) bool {
	if scalarString(value) == audience {
		return true
	}
	list, _ := value.([]any)
	for _, v := range list {
		if scalarString(v) == audience {
			return true
		}
	}
	return false
}
func numericDate(value any) (time.Time, bool) {
	var n int64
	switch v := value.(type) {
	case json.Number:
		var err error
		n, err = strconv.ParseInt(v.String(), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
	case float64:
		n = int64(v)
	default:
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}
