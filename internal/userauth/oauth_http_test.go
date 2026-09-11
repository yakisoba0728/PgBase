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
	"net/url"
	"testing"
	"time"
)

type oauthProviderStub struct{}

func (oauthProviderStub) AuthorizationURL(string, string, string) (string, error) { return "", nil }
func (oauthProviderStub) Exchange(context.Context, string, string, string) (OAuthIdentity, error) {
	return OAuthIdentity{}, nil
}

func TestConfigureOAuthRestrictsReturnURIsToWebEndpoints(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{name: "https", uri: "https://app.example/oauth/callback", want: true},
		{name: "loopback HTTP", uri: "http://localhost:3000/oauth/callback", want: true},
		{name: "non-loopback HTTP", uri: "http://app.example/oauth/callback"},
		{name: "userinfo", uri: "https://user@app.example/oauth/callback"},
		{name: "fragment", uri: "https://app.example/oauth/callback#fragment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&Service{}).ConfigureOAuth(map[string]OAuthProvider{"test": oauthProviderStub{}}, []string{test.uri}, time.Minute)
			if (err == nil) != test.want {
				t.Fatalf("ConfigureOAuth(%q) error=%v, want accepted=%t", test.uri, err, test.want)
			}
		})
	}
}

func TestHTTPOAuthProviderPKCEExchange(t *testing.T) {
	var verifier string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "bad", 400)
			return
		}
		verifier = r.Form.Get("code_verifier")
		if r.Form.Get("code") != "local-code" || r.Form.Get("redirect_uri") != "http://localhost/callback" {
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "local-token", "token_type": "Bearer"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-token" {
			http.Error(w, "bad", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "subject-1", "mail": "USER@example.test", "confirmed": true})
	})
	provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", ClientID: "client", ClientSecret: "secret", Scopes: []string{"openid", "email"}, SubjectField: "id", EmailField: "mail", EmailVerifiedField: "confirmed", HTTPClient: server.Client(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := provider.AuthorizationURL("state-value", "challenge-value", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authorization)
	if q := u.Query(); q.Get("state") != "state-value" || q.Get("code_challenge") != "challenge-value" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization query = %v", q)
	}
	identity, err := provider.Exchange(context.Background(), "local-code", "verifier-value", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if verifier != "verifier-value" || identity.Subject != "subject-1" || identity.Email != "USER@example.test" || !identity.EmailVerified {
		t.Fatalf("exchange = verifier %q identity %#v", verifier, identity)
	}
}

func TestHTTPOAuthProviderOIDCIDTokenFallbackVerifiesClaimsAndSignature(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	mux := http.NewServeMux()
	server = httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "one", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	})
	verifier := "verifier-value"
	d := sha256.Sum256([]byte(verifier))
	makeToken := func(issuer string) string {
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "one"})
		claims, _ := json.Marshal(map[string]any{"iss": issuer, "aud": "client", "exp": time.Now().Add(time.Minute).Unix(), "nonce": base64.RawURLEncoding.EncodeToString(d[:]), "sub": "subject", "email": "oidc@example.test", "email_verified": true})
		signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		sum := sha256.Sum256([]byte(signing))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"access_token": "token", "id_token": makeToken(server.URL)})
	})
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", Issuer: server.URL, JWKSURL: server.URL + "/keys", ClientID: "client", AllowInsecureLoopback: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := p.Exchange(context.Background(), "code", verifier, "http://localhost/callback")
	if err != nil || identity.Subject != "subject" {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
	if _, err := p.verifyOIDCIDToken(context.Background(), makeToken("https://wrong.example"), verifier); err == nil {
		t.Fatal("wrong issuer accepted")
	}
}

func TestOIDCIDTokenFallbackRejectsInvalidTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "one", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	})
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", Issuer: server.URL, JWKSURL: server.URL + "/keys", ClientID: "client", AllowInsecureLoopback: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	verifier := "verifier"
	digest := sha256.Sum256([]byte(verifier))
	nonce := base64.RawURLEncoding.EncodeToString(digest[:])
	sign := func(header map[string]string, claims map[string]any, signer *rsa.PrivateKey) string {
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(claims)
		input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
		d := sha256.Sum256([]byte(input))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, d[:])
		return input + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	base := func() map[string]any {
		return map[string]any{"iss": server.URL, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "sub": "subject"}
	}
	valid := sign(map[string]string{"alg": "RS256", "kid": "one"}, base(), key)
	if _, err := p.verifyOIDCIDToken(context.Background(), valid, verifier); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	multiAudience := func(azp any) map[string]any {
		c := base()
		c["aud"] = []string{"client", "other-client"}
		if azp != nil {
			c["azp"] = azp
		}
		return c
	}
	if _, err := p.verifyOIDCIDToken(context.Background(), sign(map[string]string{"alg": "RS256", "kid": "one"}, multiAudience("client"), key), verifier); err != nil {
		t.Fatalf("multi-audience token with matching azp: %v", err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := []struct {
		name  string
		token string
	}{
		{"wrong audience", sign(map[string]string{"alg": "RS256", "kid": "one"}, func() map[string]any { c := base(); c["aud"] = "other"; return c }(), key)},
		{"wrong issuer", sign(map[string]string{"alg": "RS256", "kid": "one"}, func() map[string]any { c := base(); c["iss"] = "https://wrong"; return c }(), key)},
		{"expired", sign(map[string]string{"alg": "RS256", "kid": "one"}, func() map[string]any { c := base(); c["exp"] = time.Now().Add(-time.Hour).Unix(); return c }(), key)},
		{"wrong nonce", sign(map[string]string{"alg": "RS256", "kid": "one"}, func() map[string]any { c := base(); c["nonce"] = "wrong"; return c }(), key)},
		{"bad signature", sign(map[string]string{"alg": "RS256", "kid": "one"}, base(), other)},
		{"unsupported algorithm", sign(map[string]string{"alg": "none", "kid": "one"}, base(), key)},
		{"unknown kid", sign(map[string]string{"alg": "RS256", "kid": "other"}, base(), key)},
		{"multi-audience without azp", sign(map[string]string{"alg": "RS256", "kid": "one"}, multiAudience(nil), key)},
		{"multi-audience with wrong azp", sign(map[string]string{"alg": "RS256", "kid": "one"}, multiAudience("other-client"), key)},
		{"single audience with wrong azp", sign(map[string]string{"alg": "RS256", "kid": "one"}, func() map[string]any { c := base(); c["azp"] = "other-client"; return c }(), key)},
		{"malformed", "not-a-jwt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.verifyOIDCIDToken(context.Background(), tc.token, verifier); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
