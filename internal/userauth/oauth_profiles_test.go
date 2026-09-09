package userauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderProfileNormalizers(t *testing.T) {
	tests := []struct {
		name, body, subject, email, avatar string
		verified                           bool
	}{
		{"discord", `{"id":"42","email":"d@example.test","verified":true,"avatar":"abc"}`, "42", "d@example.test", "https://cdn.discordapp.com/avatars/42/abc.png", true},
		{"facebook", `{"id":"9","email":"f@example.test","picture":{"data":{"url":"https://img.test/f"}}}`, "9", "f@example.test", "https://img.test/f", true},
		{"kakao", `{"id":9223372036854775807,"properties":{"profile_image":"https://img.test/k"},"kakao_account":{"email":"k@example.test","is_email_verified":true,"is_email_valid":true}}`, "9223372036854775807", "k@example.test", "https://img.test/k", true},
		{"patreon", `{"data":{"id":"p1","attributes":{"email":"p@example.test","is_email_verified":false,"image_url":"https://img.test/p"}}}`, "p1", "", "https://img.test/p", false},
		{"spotify", `{"id":"s1","email":"untrusted@example.test","images":[{"url":"https://img.test/s"}]}`, "s1", "", "https://img.test/s", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.body), &fields); err != nil {
				t.Fatal(err)
			}
			identity, handled, err := normalizeProviderProfile(test.name, fields, "token", &HTTPOAuthProvider{})
			if err != nil || !handled {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if identity.Subject != test.subject || identity.Email != test.email || identity.EmailVerified != test.verified || identity.AvatarURL != test.avatar {
				t.Fatalf("identity = %#v", identity)
			}
		})
	}
}

func TestGitHubProviderFetchesVerifiedPrimaryEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token", "token_type": "bearer"})
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 123, "avatar_url": serverURL(r) + "/avatar"})
		case "/user/emails":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"email": "other@example.test", "verified": true, "primary": false}, {"email": "primary@example.test", "verified": true, "primary": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: "github", AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/user", ClientID: "client", ClientSecret: "secret", AllowInsecureLoopback: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := p.Exchange(context.Background(), "code", "verifier", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "123" || identity.Email != "primary@example.test" || !identity.EmailVerified {
		t.Fatalf("identity = %#v", identity)
	}
}

func serverURL(r *http.Request) string { return "http://" + strings.TrimSpace(r.Host) }
