package userauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOIDCRejectsInactiveTokensAndNonSigningKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := "security-regression-verifier"
	digest := sha256.Sum256([]byte(verifier))
	var currentKey atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{currentKey.Load().(map[string]any)}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", Issuer: server.URL, JWKSURL: server.URL + "/keys", ClientID: "client", AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		claims func(map[string]any)
		key    func(map[string]any)
		wantOK bool
	}{
		{name: "valid", wantOK: true},
		{name: "missing expiry", claims: func(c map[string]any) { delete(c, "exp") }},
		{name: "null expiry", claims: func(c map[string]any) { c["exp"] = nil }},
		{name: "null issued at", claims: func(c map[string]any) { c["iat"] = nil }},
		{name: "fractional dates", claims: func(c map[string]any) {
			c["iat"] = float64(time.Now().Add(-time.Minute).Unix()) + 0.5
			c["exp"] = float64(time.Now().Add(time.Hour).Unix()) + 0.5
		}, wantOK: true},
		{name: "future not before", claims: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{name: "malformed not before", claims: func(c map[string]any) { c["nbf"] = "not-a-time" }},
		{name: "null not before", claims: func(c map[string]any) { c["nbf"] = nil }},
		{name: "missing issued at", claims: func(c map[string]any) { delete(c, "iat") }},
		{name: "future issued at", claims: func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }},
		{name: "malformed issued at", claims: func(c map[string]any) { c["iat"] = "not-a-time" }},
		{name: "absent optional key restrictions", key: func(k map[string]any) { delete(k, "use"); delete(k, "alg") }, wantOK: true},
		{name: "null key use", key: func(k map[string]any) { k["use"] = nil }},
		{name: "empty key use", key: func(k map[string]any) { k["use"] = "" }},
		{name: "null key algorithm", key: func(k map[string]any) { k["alg"] = nil }},
		{name: "empty key algorithm", key: func(k map[string]any) { k["alg"] = "" }},
		{name: "null key operations", key: func(k map[string]any) { k["key_ops"] = nil }},
		{name: "empty key operations", key: func(k map[string]any) { k["key_ops"] = []string{} }},
		{name: "encryption key", key: func(k map[string]any) { k["use"] = "enc" }},
		{name: "incompatible algorithm", key: func(k map[string]any) { k["alg"] = "RS512" }},
		{name: "encryption operations", key: func(k map[string]any) { k["key_ops"] = []string{"encrypt"} }},
		{name: "conflicting operations", key: func(k map[string]any) { k["key_ops"] = []string{"verify", "encrypt"} }},
		{name: "duplicate operations", key: func(k map[string]any) { k["key_ops"] = []string{"verify", "verify"} }},
		{name: "verification operations", key: func(k map[string]any) { k["key_ops"] = []string{"verify"} }, wantOK: true},
		{name: "signature operations", key: func(k map[string]any) { k["key_ops"] = []string{"sign", "verify"} }, wantOK: true},
		{name: "valid active token", claims: func(c map[string]any) { c["nbf"] = time.Now().Add(-time.Minute).Unix() }, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jwk := map[string]any{"kid": "one", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB", "use": "sig", "alg": "RS256"}
			claims := map[string]any{"iss": server.URL, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "nonce": base64.RawURLEncoding.EncodeToString(digest[:]), "sub": "subject"}
			if tc.claims != nil {
				tc.claims(claims)
			}
			if tc.key != nil {
				tc.key(jwk)
			}
			currentKey.Store(jwk)
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "one"})
			payload, _ := json.Marshal(claims)
			input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
			hash := sha256.Sum256([]byte(input))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.verifyOIDCIDToken(context.Background(), input+"."+base64.RawURLEncoding.EncodeToString(signature), verifier)
			if (err == nil) != tc.wantOK {
				t.Fatalf("accepted=%v want=%v err=%v", err == nil, tc.wantOK, err)
			}
		})
	}
}

func TestOIDCSigningKeyRejectsWeakModulusAndInvalidExponent(t *testing.T) {
	// Key parsing rejects the modulus length before signature verification;
	// fixed public integers make these boundaries deterministic.
	weak, full := make([]byte, 128), make([]byte, 256)
	weak[0], weak[len(weak)-1] = 0x80, 1
	full[0], full[len(full)-1] = 0x80, 1
	var currentKey atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{currentKey.Load().(map[string]string)}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", Issuer: server.URL, JWKSURL: server.URL + "/keys", ClientID: "client", AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, n, e string }{
		{"1024-bit modulus", base64.RawURLEncoding.EncodeToString(weak), "AQAB"},
		{"even exponent", base64.RawURLEncoding.EncodeToString(full), "BA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentKey.Store(map[string]string{"kid": "one", "kty": "RSA", "n": tc.n, "e": tc.e})
			if _, err := provider.oidcRSAKey(t.Context(), "one"); err == nil {
				t.Fatal("ineligible verification key accepted")
			}
		})
	}
}

func TestOIDCTimeBoundaries(t *testing.T) {
	now := time.Unix(1000, 500000000)
	for _, tc := range []struct {
		name, exp, iat, nbf string
		want                bool
	}{
		{"active fractional", "1000.75", "999.5", "999.75", true},
		{"expired fractional", "1000.25", "999.5", "", false},
		{"issued within allowed skew", "1061", "1060.5", "", true},
		{"issued beyond allowed skew", "1062", "1060.75", "", false},
		{"not before within allowed skew", "1061", "999", "1060.5", true},
		{"not before beyond allowed skew", "1062", "999", "1060.75", false},
		{"numeric overflow", "1e309", "999", "", false},
		{"string date", "\"1100\"", "999", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]json.RawMessage{"exp": json.RawMessage(tc.exp), "iat": json.RawMessage(tc.iat)}
			if tc.nbf != "" {
				claims["nbf"] = json.RawMessage(tc.nbf)
			}
			if got := validOIDCTimes(claims, now); got != tc.want {
				t.Fatalf("valid=%v want=%v", got, tc.want)
			}
		})
	}
}
