//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestPublicPasswordSignupAcceptsTypedProfileHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	id := uuid.New()
	name := "signup_profile_" + id.String()[:8]
	fields := []map[string]any{
		{"name": "display_name", "type": "text", "required": true},
		{"name": "balance", "type": "numeric"},
		{"name": "metadata", "type": "jsonb"},
		{"name": "joined_at", "type": "autodate", "required": true, "options": map[string]any{"onCreate": true}},
	}
	created := publicUsersRequest(t, app, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": id.String(), "name": name, "registrationEnabled": true, "fields": fields,
		"rules": map[string]any{"create": "display_name = 'Ada' && @request.body.fields:isset = true", "view": "true", "update": "true"},
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	registered := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/"+name+"/register", map[string]any{
		"email": "profile@example.test", "password": publicUserOnePass,
		"fields": map[string]any{"display_name": "Ada", "balance": json.Number("12345678901234567890.123456789"), "metadata": map[string]any{"precise": json.Number("9007199254740993")}},
	}, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	var session signupProfileSession
	decodePublicUsersJSON(t, registered, &session)
	profile := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/"+name+"/profile", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, profile, http.StatusOK)
	var result struct {
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	decodePublicUsersJSON(t, profile, &result)
	values := result.Attributes
	if string(values["balance"]) != "12345678901234567890.123456789" || string(values["display_name"]) != `"Ada"` || len(values["joined_at"]) == 0 {
		t.Fatalf("typed profile lost fields or precision: %#v", values)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(values["metadata"], &metadata); err != nil || string(metadata["precise"]) != "9007199254740993" {
		t.Fatalf("JSON profile lost precision: %s %v", values["metadata"], err)
	}
}

func TestDefaultSignupProfileInputAndMalformedBodiesHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	updated := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/auth-collections/00000000-0000-0000-0000-000000000001", map[string]any{
		"registrationEnabled": true, "fields": []map[string]any{{"name": "display_name", "type": "text", "required": true}},
		"rules": map[string]any{"create": "true", "view": "true"},
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, updated, http.StatusOK)
	for _, fields := range []any{nil, []any{}, "wrong", true} {
		response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register", map[string]any{"email": "default-profile@example.test", "password": publicUserOnePass, "fields": fields}, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusBadRequest)
	}
	for _, key := range []string{"verified", "active", "id"} {
		response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register", map[string]any{"email": "default-profile@example.test", "password": publicUserOnePass, "fields": map[string]any{"display_name": "Default"}, key: true}, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusBadRequest)
	}
	registered := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register?fields=user.id,user.verified,token", map[string]any{"email": "default-profile@example.test", "password": publicUserOnePass, "fields": map[string]any{"display_name": "Default"}}, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	var session signupProfileSession
	decodePublicUsersJSON(t, registered, &session)
	if session.Token == "" || session.User.Verified {
		t.Fatalf("default signup authority or response projection: %#v", session)
	}
	profile := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/users/profile", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, profile, http.StatusOK)
	var body struct {
		Attributes map[string]any `json:"attributes"`
	}
	decodePublicUsersJSON(t, profile, &body)
	if body.Attributes["display_name"] != "Default" {
		t.Fatalf("default profile=%#v", body)
	}
	login := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "default-profile@example.test", "password": publicUserOnePass, "fields": map[string]any{}}, "", nil, nil)
	assertPublicUsersStatus(t, login, http.StatusBadRequest)
}

func TestOAuthSignupAcceptsProfileThroughMethodsEndpointHTTP(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "profile-access", "token_type": "Bearer"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "profile-subject", "email": "oauth-profile@example.test", "email_verified": true})
	}))
	defer provider.Close()
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.OAuthProviders = []OAuthProviderOptions{{Name: "local", AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", UserInfoURL: provider.URL + "/userinfo", ClientID: "profile-client", ClientSecret: "profile-secret", AllowInsecureLoopback: true}}
		options.OAuthRedirects = []string{"http://localhost/oauth/callback"}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	id := uuid.New()
	name := "oauth_profile_" + id.String()[:8]
	created := publicUsersRequest(t, app, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": id.String(), "name": name, "registrationEnabled": true,
		"fields": []map[string]any{{"name": "display_name", "type": "text", "required": true}},
		"rules":  map[string]any{"create": "display_name = 'OAuth Profile'", "view": "true"},
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	methods := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/"+name+"/methods", nil, "", nil, nil)
	assertPublicUsersStatus(t, methods, http.StatusOK)
	var methodBody struct {
		OAuth2 struct {
			Providers []struct {
				StartEndpoint string `json:"startEndpoint"`
			} `json:"providers"`
		} `json:"oauth2"`
	}
	decodePublicUsersJSON(t, methods, &methodBody)
	if len(methodBody.OAuth2.Providers) != 1 {
		t.Fatalf("provider metadata=%#v", methodBody)
	}
	start := publicUsersRequest(t, app, http.MethodPost, methodBody.OAuth2.Providers[0].StartEndpoint, map[string]any{"provider": "local", "redirectUri": "http://localhost/oauth/callback"}, "", nil, nil)
	assertPublicUsersStatus(t, start, http.StatusOK)
	var authorization struct {
		State string `json:"state"`
	}
	decodePublicUsersJSON(t, start, &authorization)
	input := map[string]any{"provider": "local", "state": authorization.State, "code": "profile-code", "fields": map[string]any{"display_name": "Denied"}}
	denied := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/oauth/complete", input, "", nil, nil)
	assertPublicUsersStatus(t, denied, http.StatusUnauthorized)
	input["fields"] = map[string]any{"display_name": "OAuth Profile"}
	complete := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/oauth/complete", input, "", nil, nil)
	assertPublicUsersStatus(t, complete, http.StatusOK)
	var session signupProfileSession
	decodePublicUsersJSON(t, complete, &session)
	profile := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/"+name+"/profile", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, profile, http.StatusOK)
	var body struct {
		Attributes map[string]any `json:"attributes"`
	}
	decodePublicUsersJSON(t, profile, &body)
	if body.Attributes["display_name"] != "OAuth Profile" || !session.User.Verified {
		t.Fatalf("OAuth profile authority=%#v %#v", body, session)
	}
}

type signupProfileSession struct {
	Token string `json:"token"`
	User  struct {
		ID       uuid.UUID `json:"id"`
		Verified bool      `json:"verified"`
	} `json:"user"`
}
