//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

func TestAPIRateLimitClassifiesAdmittedServiceUserAdminAndGuest(t *testing.T) {
	respond := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.CustomRoutes = []CustomRoute{
			{Method: http.MethodGet, Pattern: "/reports/rate/service-auth", Access: RouteService, Handler: respond},
			{Method: http.MethodGet, Pattern: "/reports/rate/service-guest", Access: RouteService, Handler: respond},
			{Method: http.MethodGet, Pattern: "/reports/rate/guest", Access: RoutePublic, Handler: respond},
			{Method: http.MethodGet, Pattern: "/reports/rate/user", Access: RouteUser, Handler: respond},
			{Method: http.MethodGet, Pattern: "/reports/rate/admin", Access: RouteAdmin, Handler: respond},
		}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "rate_limit_service_scope")
	var collectionID uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='rate_limit_service_scope'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	configureAPIRateRules(t, fixture, []runtimesettings.APIRateRule{
		{Label: "/reports/rate/service-auth", Audience: "@auth", Duration: 60, MaxRequests: 1},
		{Label: "/reports/rate/service-guest", Audience: "@guest", Duration: 60, MaxRequests: 1},
		{Label: "/reports/rate/guest", Audience: "@guest", Duration: 60, MaxRequests: 1},
		{Label: "/reports/rate/user", Audience: "@auth", Duration: 60, MaxRequests: 1},
		{Label: "/reports/rate/admin", Audience: "@auth", Duration: 60, MaxRequests: 1},
	})

	issued := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/service-tokens", map[string]any{
		"name": "rate-limit service", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}},
	}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, http.StatusCreated)
	var service struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &service)
	if service.Token == "" {
		t.Fatal("service token missing")
	}

	// Service admission happens before limiting: a rejected credential must not
	// consume the service's authenticated quota.
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/service-auth", nil, "invalid-service-token", nil, nil), http.StatusUnauthorized)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/service-auth", nil, service.Token, nil, nil), http.StatusNoContent)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/service-auth", nil, service.Token, nil, nil), http.StatusTooManyRequests)

	// A service identity is @auth, so an @guest-only rule must not throttle it.
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/service-guest", nil, service.Token, nil, nil), http.StatusNoContent)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/service-guest", nil, service.Token, nil, nil), http.StatusNoContent)

	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/guest", nil, "", nil, nil), http.StatusNoContent)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/guest", nil, "", nil, nil), http.StatusTooManyRequests)

	user := registerPublicUser(t, fixture, "rate-limit-user@example.test", publicUserOnePass)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/user", nil, user.Token, nil, nil), http.StatusNoContent)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/user", nil, user.Token, nil, nil), http.StatusTooManyRequests)

	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/admin", nil, "", cookie, nil), http.StatusNoContent)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/reports/rate/admin", nil, "", cookie, nil), http.StatusTooManyRequests)
}

func configureAPIRateRules(t *testing.T, fixture *publicUsersFixture, rules []runtimesettings.APIRateRule) {
	t.Helper()
	settings := fixture.app.runtimeSettings.Get()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = rules
	if _, err := fixture.app.runtimeSettings.Update(context.Background(), settings); err != nil {
		t.Fatalf("update API rate settings: %v", err)
	}
}
