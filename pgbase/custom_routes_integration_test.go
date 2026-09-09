//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestCustomRoutesThroughHTTPWithSessionRevocation(t *testing.T) {
	registry := &HookRegistry{}
	var observed atomic.Int64
	_, err := registry.On(HookRequest, Hook{After: func(context.Context, *HookEvent) { observed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			http.Error(w, "missing principal", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(principal)
	})
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.Hooks = registry
		o.CustomRoutes = []CustomRoute{
			{Method: "GET", Pattern: "/reports/admin", Access: RouteAdmin, Handler: handler},
			{Method: "POST", Pattern: "/reports/admin", Access: RouteAdmin, Handler: handler},
			{Method: "GET", Pattern: "/reports/user", Access: RouteUser, Handler: handler},
			{Method: "GET", Pattern: "/reports/service", Access: RouteService, Handler: handler},
		}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/admin", nil, "", nil, nil), 401)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/admin", nil, "", cookie, map[string]string{"Origin": "https://other.test"}), 403)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/reports/admin", nil, "", cookie, nil), 403)
	admin := publicUsersRequest(t, fixture, "POST", "/reports/admin", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, admin, 200)
	var adminPrincipal RequestPrincipal
	decodePublicUsersJSON(t, admin, &adminPrincipal)
	if adminPrincipal.Kind != "administrator" || adminPrincipal.ID == "" || strings.Contains(admin.Body.String(), cookie.Value) || strings.Contains(admin.Body.String(), csrf) {
		t.Fatal("incorrect administrator principal projection")
	}
	registered := publicUsersRequest(t, fixture, "POST", "/api/v1/auth/register", map[string]any{"email": "routes@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, 201)
	var session publicUserSession
	decodePublicUsersJSON(t, registered, &session)
	user := publicUsersRequest(t, fixture, "GET", "/reports/user", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, user, 200)
	var principal RequestPrincipal
	decodePublicUsersJSON(t, user, &principal)
	if principal.Kind != "user" || principal.ID != session.User.ID.String() || principal.AuthCollectionID == "" || strings.Contains(user.Body.String(), session.Token) {
		t.Fatal("incorrect user identity projection")
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/admin", nil, session.Token, nil, nil), 401)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/user", nil, "", cookie, nil), 401)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/auth/logout", nil, session.Token, nil, nil), 204)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/user", nil, session.Token, nil, nil), 401)

	createPublicUsersCollection(t, fixture, cookie, csrf, "custom_service_records")
	var collectionID uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='custom_service_records'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	issued := publicUsersRequest(t, fixture, "POST", "/api/admin/service-tokens", map[string]any{"name": "custom route worker", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, 201)
	var token struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &token)
	service := publicUsersRequest(t, fixture, "GET", "/reports/service", nil, token.Token, nil, nil)
	assertPublicUsersStatus(t, service, 200)
	decodePublicUsersJSON(t, service, &principal)
	if principal.Kind != "service" || principal.ID != token.ID || strings.Contains(service.Body.String(), token.Token) {
		t.Fatal("incorrect service principal projection")
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/user", nil, token.Token, nil, nil), 401)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/admin/service-tokens/"+token.ID+"/revoke", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 204)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/service", nil, token.Token, nil, nil), 401)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", "/api/admin/session", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 204)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/reports/admin", nil, "", cookie, nil), 401)
	if observed.Load() < 10 {
		t.Fatal("custom requests did not pass application request hooks")
	}
}
