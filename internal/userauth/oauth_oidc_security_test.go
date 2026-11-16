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
		{name: "future not before", claims: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{name: "malformed not before", claims: func(c map[string]any) { c["nbf"] = "not-a-time" }},
		{name: "null not before", claims: func(c map[string]any) { c["nbf"] = nil }},
		{name: "missing issued at", claims: func(c map[string]any) { delete(c, "iat") }},
		{name: "future issued at", claims: func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }},
		{name: "malformed issued at", claims: func(c map[string]any) { c["iat"] = "not-a-time" }},
		{name: "encryption key", key: func(k map[string]any) { k["use"] = "enc" }},
		{name: "incompatible algorithm", key: func(k map[string]any) { k["alg"] = "RS512" }},
		{name: "encryption operations", key: func(k map[string]any) { k["key_ops"] = []string{"encrypt"} }},
		{name: "verification operations", key: func(k map[string]any) { k["key_ops"] = []string{"verify"} }, wantOK: true},
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
