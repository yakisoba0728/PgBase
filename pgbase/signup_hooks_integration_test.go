//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSignupProfileHooksWrapHTTPInputs(t *testing.T) {
	registry := &HookRegistry{}
	blocked := errors.New("signup hook veto")
	veto, before, after := true, 0, 0
	var afterEvent AuthEvent
	for _, kind := range []string{HookAuthPassword, HookAuthOAuthComplete} {
		_, err := registry.On(kind, Hook{
			Before: func(_ context.Context, event *HookEvent) error {
				before++
				raw, _ := json.Marshal(event.Data)
				for _, secret := range []string{"SIGNUP_PROFILE_SECRET", publicUserOnePass, "@example.test", "state-secret", "code-secret"} {
					if strings.Contains(string(raw), secret) {
						t.Errorf("hook exposed request secret %q", secret)
					}
				}
				if veto {
					return blocked
				}
				return nil
			},
			After: func(_ context.Context, event *HookEvent) {
				after++
				if veto && !errors.Is(event.Err, blocked) {
					t.Errorf("after lost veto: %v", event.Err)
				}
				afterEvent = *event.Data.(*AuthEvent)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"hook-access","token_type":"Bearer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"sub":"hook-subject","email":"oauth-hook@example.test","email_verified":true}`))
	}))
	defer provider.Close()
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Hooks = registry
		options.OAuthProviders = []OAuthProviderOptions{{Name: "local", AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", UserInfoURL: provider.URL + "/userinfo", ClientID: "hook-client", ClientSecret: "hook-secret", AllowInsecureLoopback: true}}
		options.OAuthRedirects = []string{"http://localhost/oauth/callback"}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	realm := uuid.New()
	fields := []map[string]any{{"name": "display_name", "type": "text"}}
	for _, definition := range []struct {
		method, path string
		input        map[string]any
	}{
		{http.MethodPatch, "/api/admin/auth-collections/00000000-0000-0000-0000-000000000001", map[string]any{"registrationEnabled": true, "fields": fields, "rules": map[string]any{"create": "true"}}},
		{http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realm, "name": "signup_hooks", "registrationEnabled": true, "fields": fields, "rules": map[string]any{"create": "true"}}},
	} {
		response := publicUsersRequest(t, app, definition.method, definition.path, definition.input, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
		if response.Code >= 300 {
			t.Fatalf("configure hook fixture: %d %s", response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/signup_hooks/register", "/api/v1/auth/oauth/complete"} {
		for _, presence := range []string{"omitted", "empty", "profile"} {
			t.Run(path+"/"+presence, func(t *testing.T) {
				input := map[string]any{"email": fmt.Sprintf("hook-%d@example.test", before), "password": publicUserOnePass}
				if strings.HasSuffix(path, "oauth/complete") {
					input = map[string]any{"provider": "local", "state": "state-secret", "code": "code-secret"}
				}
				if presence == "empty" {
					input["fields"] = map[string]any{}
				}
				if presence == "profile" {
					input["fields"] = map[string]any{"display_name": "SIGNUP_PROFILE_SECRET"}
				}
				previousBefore, previousAfter := before, after
				response := publicUsersRequest(t, app, http.MethodPost, path, input, "", nil, nil)
				assertPublicUsersStatus(t, response, http.StatusServiceUnavailable)
				if before != previousBefore+1 || after != previousAfter+1 {
					t.Fatalf("hooks did not pair once: before=%d after=%d", before-previousBefore, after-previousAfter)
				}
			})
		}
	}
	var users, sessions, identities int
	if err := app.app.pool.QueryRow(context.Background(), `select (select count(*) from pgbase_meta.users),(select count(*) from pgbase_meta.user_sessions),(select count(*) from pgbase_meta.user_oauth_identities)`).Scan(&users, &sessions, &identities); err != nil || users != 0 || sessions != 0 || identities != 0 {
		t.Fatalf("veto persisted auth data: %d/%d/%d %v", users, sessions, identities, err)
	}
	veto = false
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/signup_hooks/register"} {
		previousBefore, previousAfter := before, after
		response := publicUsersRequest(t, app, http.MethodPost, path, map[string]any{"email": fmt.Sprintf("allowed-%d@example.test", before), "password": publicUserOnePass, "fields": map[string]any{"display_name": "SIGNUP_PROFILE_SECRET"}}, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusCreated)
		if before != previousBefore+1 || after != previousAfter+1 || afterEvent.Session == nil || afterEvent.UserID == uuid.Nil {
			t.Fatalf("successful signup hook projection: before=%d after=%d event=%#v", before, after, afterEvent)
		}
	}
	start := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/signup_hooks/oauth/start", map[string]any{"provider": "local", "redirectUri": "http://localhost/oauth/callback"}, "", nil, nil)
	assertPublicUsersStatus(t, start, http.StatusOK)
	var authorization struct {
		State string `json:"state"`
	}
	decodePublicUsersJSON(t, start, &authorization)
	previousBefore, previousAfter := before, after
	complete := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/oauth/complete", map[string]any{"provider": "local", "state": authorization.State, "code": "code-secret", "fields": map[string]any{"display_name": "SIGNUP_PROFILE_SECRET"}}, "", nil, nil)
	assertPublicUsersStatus(t, complete, http.StatusOK)
	if before != previousBefore+1 || after != previousAfter+1 || afterEvent.Session == nil || afterEvent.RealmID != realm || afterEvent.Provider != "local" {
		t.Fatalf("OAuth completion hook projection: before=%d after=%d event=%#v", before, after, afterEvent)
	}
}
