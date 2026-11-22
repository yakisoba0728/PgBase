//go:build integration

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAdministratorLoginNetworkLimitAggregatesIPv6Within64HTTP(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "admin-ipv6-login-setup-token-0123456789"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	if _, err := auth.Setup(context.Background(), setupToken, "admin@example.test", securityPassword, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := auth.ConfigureRatePolicy(adminauth.RatePolicy{AccountMaxFailures: 50, NetworkMaxFailures: 2, Window: time.Hour, Block: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AllowIP(context.Background(), "admin@example.test", "2001:db8:9:2::a/128"); err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})
	login := func(remote, password string) *httptest.ResponseRecorder {
		t.Helper()
		request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/login", `{"email":"admin@example.test","password":"`+password+`"}`)
		request.Header.Set("Origin", "http://pgbase.test")
		request.RemoteAddr = "[" + remote + "]:40000"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	// Rotating addresses inside one /64 shares one network bucket.
	for _, remote := range []string{"2001:db8:9:1::1", "2001:db8:9:1::2"} {
		assertSecurityError(t, login(remote, "incorrect horse battery staple"), http.StatusUnauthorized, "invalid_credentials")
	}
	assertSecurityError(t, login("2001:db8:9:1:ffff::3", "incorrect horse battery staple"), http.StatusTooManyRequests, "rate_limited")

	// A different /64 has its own bucket; the allowlist still sees the exact
	// address, so a neighbour of the allowed address in that /64 is refused.
	assertSecurityError(t, login("2001:db8:9:2::b", securityPassword), http.StatusUnauthorized, "invalid_credentials")
	if response := login("2001:db8:9:2::a", securityPassword); response.Code != http.StatusOK {
		t.Fatalf("exact allowed address status=%d body=%s", response.Code, response.Body.String())
	}
}
