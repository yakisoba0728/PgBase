package userauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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
