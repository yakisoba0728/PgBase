//go:build integration

package pgbase

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestAuthRecordHTTPRulesUseActualRequestMetadata(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realm := uuid.New()
	name := "request_metadata_members"
	rules := func(rule string) map[string]any {
		return map[string]any{"create": "", "auth": "", "list": rule, "view": rule, "update": "false", "delete": "false", "manage": nil}
	}
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": realm.String(), "name": name, "registrationEnabled": true, "fields": []any{}, "rules": rules(""),
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	registered := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/"+name+"/register", map[string]any{"email": "request-metadata@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	var session publicUserSession
	decodePublicUsersJSON(t, registered, &session)
	for _, test := range []struct {
		name, rule, query string
		headers           map[string]string
		allowed           bool
	}{
		{name: "method_negation_denies_get", rule: "@request.method != 'GET'"},
		{name: "method_equality_allows_get", rule: "@request.method = 'GET'", allowed: true},
		{name: "literal_false_denies", rule: "false"},
		{name: "empty_rule_allows", rule: "", allowed: true},
		{name: "header_negation_denies", rule: "@request.headers.x_scope != 'blocked'", headers: map[string]string{"X-Scope": "blocked"}},
		{name: "header_equality_allows", rule: "@request.headers.x_scope = 'allowed'", headers: map[string]string{"X-Scope": "allowed"}, allowed: true},
		{name: "query_negation_denies", rule: "@request.query.scope != 'blocked'", query: "?scope=blocked"},
		{name: "query_equality_allows", rule: "@request.query.scope = 'allowed'", query: "?scope=allowed", allowed: true},
		{name: "nested_negation_denies", rule: "@request.method != 'GET' || @request.headers.x_scope != 'blocked'", headers: map[string]string{"X-Scope": "blocked"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated := publicUsersRequest(t, fixture, http.MethodPatch, "/api/admin/auth-collections/"+realm.String(), map[string]any{"registrationEnabled": true, "rules": rules(test.rule)}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
			assertPublicUsersStatus(t, updated, http.StatusOK)
			for _, caller := range []struct{ name, base, token string }{
				{"anonymous", "/api/v1/public/auth/" + name + "/records", ""},
				{"authenticated", "/api/v1/auth/" + name + "/records", session.Token},
			} {
				t.Run(caller.name, func(t *testing.T) {
					view := publicUsersRequest(t, fixture, http.MethodGet, caller.base+"/"+session.User.ID.String()+test.query, nil, caller.token, nil, test.headers)
					wantStatus, wantItems := http.StatusUnauthorized, 0
					if test.allowed {
						wantStatus, wantItems = http.StatusOK, 1
					}
					assertPublicUsersStatus(t, view, wantStatus)
					list := publicUsersRequest(t, fixture, http.MethodGet, caller.base+test.query, nil, caller.token, nil, test.headers)
					assertPublicUsersStatus(t, list, http.StatusOK)
					var page struct {
						Items []map[string]any `json:"items"`
					}
					decodePublicUsersJSON(t, list, &page)
					if len(page.Items) != wantItems {
						t.Fatalf("rule %q returned %d records, want %d", test.rule, len(page.Items), wantItems)
					}
					if wantItems == 1 && page.Items[0]["id"] != session.User.ID.String() {
						t.Fatalf("unexpected allowed record: %#v", page.Items)
					}
				})
			}
		})
	}
}

func TestAuthSessionHTTPRulesUseActualRequestMetadata(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realm := uuid.New()
	name := "request_session_members"
	rules := map[string]any{"create": "", "auth": "", "list": "false", "view": "false", "update": "false", "delete": "false", "manage": nil}
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": realm.String(), "name": name, "registrationEnabled": true, "fields": []any{}, "rules": rules,
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	credentials := map[string]any{"email": "request-session@example.test", "password": publicUserOnePass}
	registered := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/"+name+"/register", credentials, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	for _, test := range []struct {
		name, rule, query string
		headers           map[string]string
		allowed           bool
	}{
		{name: "method_negation", rule: "@request.method != 'POST'"},
		{name: "method_equality", rule: "@request.method = 'POST'", allowed: true},
		{name: "header_negation", rule: "@request.headers.x_scope != 'blocked'", headers: map[string]string{"X-Scope": "blocked"}},
		{name: "header_equality", rule: "@request.headers.x_scope = 'allowed'", headers: map[string]string{"X-Scope": "allowed"}, allowed: true},
		{name: "query_negation", rule: "@request.query.scope != 'blocked'", query: "?scope=blocked"},
		{name: "query_equality", rule: "@request.query.scope = 'allowed'", query: "?scope=allowed", allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rules["auth"] = test.rule
			updated := publicUsersRequest(t, fixture, http.MethodPatch, "/api/admin/auth-collections/"+realm.String(), map[string]any{"registrationEnabled": true, "rules": rules}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
			assertPublicUsersStatus(t, updated, http.StatusOK)
			login := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/"+name+"/login"+test.query, credentials, "", nil, test.headers)
			want := http.StatusUnauthorized
			if test.allowed {
				want = http.StatusOK
			}
			assertPublicUsersStatus(t, login, want)
		})
	}
}
