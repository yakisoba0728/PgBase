package userauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type oauthContractTransport func(*http.Request) (*http.Response, error)

func (f oauthContractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Controlled provider contracts, not logins against external provider accounts.
func TestSupportedOAuthProviderHTTPContracts(t *testing.T) {
	tests := []struct{ name, provider, profile, subject, email string }{
		{"google", "google", `{"sub":"subject","email":"person@example.test","email_verified":true}`, "subject", "person@example.test"},
		{"oidc", "oidc", `{"sub":"subject","email":"person@example.test","email_verified":true}`, "subject", "person@example.test"},
		{"discord", "discord", `{"id":"subject","email":"person@example.test","verified":true}`, "subject", "person@example.test"},
		{"facebook", "facebook", `{"id":"subject","email":"person@example.test"}`, "subject", "person@example.test"},
		{"bitbucket", "bitbucket", `{"uuid":"subject"}`, "subject", "person@example.test"},
		{"box", "box", `{"id":"subject","login":"person@example.test"}`, "subject", "person@example.test"},
		{"gitea", "gitea", `{"id":42}`, "42", "person@example.test"},
		{"gitee", "gitee", `{"id":42,"email":"person@example.test"}`, "42", "person@example.test"},
		{"github", "github", `{"id":42}`, "42", "person@example.test"},
		{"gitlab", "gitlab", `{"id":42,"email":"person@example.test"}`, "42", "person@example.test"},
		{"instagram2", "instagram2", `{"id":"subject"}`, "subject", ""},
		{"kakao", "kakao", `{"id":42,"kakao_account":{"email":"person@example.test","is_email_verified":true,"is_email_valid":true}}`, "42", "person@example.test"},
		{"lark", "lark", `{"data":{"union_id":"subject"}}`, "subject", ""},
		{"linear", "linear", `{"data":{"viewer":{"id":"subject","email":"person@example.test"}}}`, "subject", "person@example.test"},
		{"livechat", "livechat", `{"id":"subject","email":"person@example.test"}`, "subject", "person@example.test"},
		{"mailcow", "mailcow", `{"id":"subject","email":"person@example.test"}`, "subject", "person@example.test"},
		{"microsoft", "microsoft", `{"id":"subject","mail":"person@example.test"}`, "subject", "person@example.test"},
		{"monday", "monday", `{"data":{"me":{"id":42,"email":"person@example.test","is_verified":true}}}`, "42", "person@example.test"},
		{"notion", "notion", `{"bot":{"owner":{"user":{"id":"subject","person":{"email":"person@example.test"}}}}}`, "subject", "person@example.test"},
		{"patreon", "patreon", `{"data":{"id":"subject","attributes":{"email":"person@example.test","is_email_verified":true}}}`, "subject", "person@example.test"},
		{"planningcenter", "planningcenter", `{"data":{"id":"subject","attributes":{"primary_contact_email":"person@example.test"}}}`, "subject", "person@example.test"},
		{"spotify", "spotify", `{"id":"subject","email":"untrusted@example.test"}`, "subject", ""},
		{"strava", "strava", `{"id":42}`, "42", ""},
		{"trakt", "trakt", `{"user":{"ids":{"slug":"subject"}}}`, "subject", ""},
		{"twitch", "twitch", `{"data":[{"id":"subject","email":"person@example.test"}]}`, "subject", "person@example.test"},
		{"twitter", "twitter", `{"data":{"id":"subject","confirmed_email":"person@example.test"}}`, "subject", "person@example.test"},
		{"vk", "vk", `{"response":[{"id":42}],"email":"person@example.test"}`, "42", "person@example.test"},
		{"wakatime", "wakatime", `{"data":{"id":"subject","email":"person@example.test","is_email_confirmed":true}}`, "subject", "person@example.test"},
		{"yandex", "yandex", `{"id":"subject","default_email":"person@example.test"}`, "subject", "person@example.test"},
		{"microsoft-oidc", "microsoft", `{"sub":"subject","email":"person@example.test"}`, "subject", ""},
	}
	covered := map[string]bool{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			covered[test.provider] = true
			var tokenCalls, profileCalls, emailCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/token":
					tokenCalls++
					if r.Method != http.MethodPost {
						t.Errorf("token method=%s", r.Method)
					}
					if test.provider == "github" && r.Header.Get("Accept") != "application/json" {
						w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
						_, _ = w.Write([]byte("access_token=token&token_type=bearer"))
						return
					}
					body := map[string]string{}
					if test.provider == "notion" {
						if r.Header.Get("Content-Type") != "application/json" {
							http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
							return
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
					} else {
						if err := r.ParseForm(); err != nil {
							t.Error(err)
						}
						for name := range r.PostForm {
							body[name] = r.PostForm.Get(name)
						}
					}
					for name, want := range map[string]string{"grant_type": "authorization_code", "code": "code", "redirect_uri": "https://app.example/callback", "code_verifier": "verifier"} {
						if body[name] != want {
							t.Errorf("token %s=%q want=%q", name, body[name], want)
						}
					}
					if test.provider == "bitbucket" || test.provider == "notion" {
						user, password, ok := r.BasicAuth()
						if !ok || user != "client" || password != "secret" {
							t.Error("missing Basic token credentials")
						}
						if body["client_id"] != "" || body["client_secret"] != "" {
							t.Error("Basic credentials duplicated in body")
						}
					} else if body["client_id"] != "client" || body["client_secret"] != "secret" {
						t.Error("missing form client credentials")
					}
					_, _ = w.Write([]byte(`{"access_token":"token","token_type":"Bearer"}`))
				case "/userinfo":
					profileCalls++
					if r.Header.Get("Authorization") != "Bearer token" {
						t.Error("missing profile bearer")
					}
					switch test.provider {
					case "notion":
						if r.Header.Get("Notion-Version") == "" {
							http.Error(w, "Notion-Version required", http.StatusBadRequest)
							return
						}
					case "twitch":
						if r.Header.Get("Client-Id") != "client" {
							t.Error("missing Twitch Client-Id")
						}
					case "trakt":
						if r.Header.Get("trakt-api-key") != "client" || r.Header.Get("trakt-api-version") != "2" {
							t.Error("missing Trakt headers")
						}
					}
					if test.provider == "linear" || test.provider == "monday" {
						var body map[string]string
						if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
							t.Error("missing GraphQL JSON POST")
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						field := "viewer"
						if test.provider == "monday" {
							field = "me"
						}
						if !strings.Contains(body["query"], field+" {") {
							t.Errorf("wrong GraphQL query: %s", body["query"])
						}
					} else if r.Method != http.MethodGet {
						t.Errorf("profile method=%s", r.Method)
					}
					_, _ = w.Write([]byte(test.profile))
				case "/user/emails", "/api/v1/user/emails", "/2.0/user/emails":
					emailCalls++
					if r.Header.Get("Authorization") != "Bearer token" {
						t.Error("missing email bearer")
					}
					response := `[{"email":"untrusted@example.test","verified":false,"primary":true},{"email":"person@example.test","verified":true,"primary":true}]`
					if test.provider == "bitbucket" {
						response = `{"values":` + response + `}`
					}
					_, _ = w.Write([]byte(response))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport
			target, _ := url.Parse(server.URL)
			// Bitbucket's fixed public email endpoint is routed only to this test server.
			client := &http.Client{Transport: oauthContractTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.bitbucket.org" {
					clone := r.Clone(r.Context())
					u := *r.URL
					u.Scheme, u.Host = target.Scheme, target.Host
					clone.URL = &u
					return transport.RoundTrip(clone)
				}
				if r.URL.Host != target.Host {
					return nil, fmt.Errorf("external request rejected: %s", r.URL.Host)
				}
				return transport.RoundTrip(r)
			})}
			provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: test.provider, AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", ClientID: "client", ClientSecret: "secret", AllowInsecureLoopback: true, HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			authorization, err := provider.AuthorizationURL("state", "challenge", "https://app.example/callback")
			if err != nil {
				t.Fatal(err)
			}
			parsed, _ := url.Parse(authorization)
			q := parsed.Query()
			if q.Get("state") != "state" || q.Get("code_challenge") != "challenge" || q.Get("code_challenge_method") != "S256" {
				t.Fatalf("authorization binding=%v", q)
			}
			identity, err := provider.Exchange(context.Background(), "code", "verifier", "https://app.example/callback")
			if err != nil {
				t.Fatal(err)
			}
			if identity.Subject != test.subject || identity.Email != test.email || identity.EmailVerified != (test.email != "") {
				t.Errorf("identity=%#v", identity)
			}
			if tokenCalls != 1 || profileCalls != 1 {
				t.Errorf("token/profile calls=%d/%d", tokenCalls, profileCalls)
			}
			wantEmails := 0
			if test.provider == "github" || test.provider == "gitea" || test.provider == "bitbucket" {
				wantEmails = 1
			}
			if emailCalls != wantEmails {
				t.Errorf("email calls=%d want=%d", emailCalls, wantEmails)
			}
		})
	}
	for name := range providerProfileRules {
		if !covered[name] {
			t.Errorf("provider missing HTTP contract fixture: %s", name)
		}
	}
}
