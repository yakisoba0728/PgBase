//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAdministratorProfileGuardsLockoutAndCredentialChanges(t *testing.T) {
	pool := newSecurityDatabase(t)
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "profile-setup-token", time.Hour, nil)
	session, err := auth.Setup(context.Background(), "profile-setup-token", "owner@example.test", securityPassword, "profile-setup")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test"), TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	raw, csrf := session.RawToken, session.CSRFToken
	request := func(method, path, body, remote, forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://pgbase.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://pgbase.test")
		r.Header.Set("X-PgBase-CSRF", csrf)
		r.AddCookie(&http.Cookie{Name: adminCookieName, Value: raw})
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const allowlist = "/api/admin/profile/ip-allowlist"
	for _, tc := range []struct {
		name, remote, forwarded, prefixes string
		status                            int
		address                           string
	}{
		{"direct excluded", "192.0.2.1:1", "", `["198.51.100.0/24"]`, 409, "192.0.2.1"},
		{"trusted proxy excluded", "10.0.0.5:1", "198.51.100.7", `["192.0.2.0/24"]`, 409, "198.51.100.7"},
		{"untrusted proxy header ignored", "198.51.100.7:1", "192.0.2.1", `["192.0.2.0/24"]`, 409, "198.51.100.7"},
		{"trusted proxy single address", "10.0.0.5:1", "198.51.100.7", `["198.51.100.7/32"]`, 200, "198.51.100.7"},
		{"trusted proxy adds IPv6", "10.0.0.5:1", "198.51.100.7", `["2001:db8::/64","198.51.100.7/32"]`, 200, "198.51.100.7"},
		{"IPv6 CIDR", "[2001:db8::7]:1", "", `["2001:db8::/64"]`, 200, "2001:db8::7"},
		{"IPv6 excluded", "[2001:db8::7]:1", "", `["2001:db8:1::/64"]`, 409, "2001:db8::7"},
		{"clearing", "[2001:db8::7]:1", "", `[]`, 200, "2001:db8::7"},
		{"direct IPv4 CIDR", "192.0.2.1:1", "", `["192.0.2.0/24"]`, 200, "192.0.2.1"},
	} {
		got := request(http.MethodPatch, allowlist, `{"currentPassword":"`+securityPassword+`","prefixes":`+tc.prefixes+`}`, tc.remote, tc.forwarded)
		if got.Code != tc.status {
			t.Fatalf("%s: status=%d body=%s", tc.name, got.Code, got.Body.String())
		}
		read := request(http.MethodGet, allowlist, "", tc.remote, tc.forwarded)
		var body struct {
			Prefixes      []string `json:"prefixes"`
			ClientAddress string   `json:"clientAddress"`
		}
		if err := json.Unmarshal(read.Body.Bytes(), &body); read.Code != 200 || err != nil || body.ClientAddress != tc.address {
			t.Fatalf("%s: GET status=%d body=%s", tc.name, read.Code, read.Body.String())
		}
	}
	if got := request(http.MethodPatch, allowlist, `{"currentPassword":"`+securityPassword+`","prefixes":["198.51.100.0/24"]}`, "192.0.2.1:1", ""); !strings.Contains(got.Body.String(), `"ip_allowlist_excludes_caller"`) {
		t.Fatalf("refusal body=%s", got.Body.String())
	}

	const profile = "/api/admin/profile"
	self := request(http.MethodPost, "/api/admin/administrators/"+session.Administrator.ID.String()+"/password-reset", `{"currentPassword":"`+securityPassword+`","newPassword":"hijacked administration password"}`, "192.0.2.1:1", "")
	if self.Code != http.StatusConflict {
		t.Fatalf("self password reset status=%d body=%s", self.Code, self.Body.String())
	}
	for _, body := range []string{
		`{"newPassword":"hijacked administration password"}`,
		`{"currentPassword":"incorrect battery staple","newPassword":"hijacked administration password"}`,
		`{"currentPassword":"incorrect battery staple","email":"hijacker@example.test"}`,
	} {
		got := request(http.MethodPatch, profile, body, "192.0.2.1:1", "")
		if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), `"current_password_invalid"`) {
			t.Fatalf("%s: status=%d body=%s", body, got.Code, got.Body.String())
		}
	}
	if got := request(http.MethodGet, profile, "", "192.0.2.1:1", ""); got.Code != 200 || !strings.Contains(got.Body.String(), "owner@example.test") {
		t.Fatalf("refused changes altered the profile or ended the session: %d %s", got.Code, got.Body.String())
	}
	changed := request(http.MethodPatch, profile, `{"currentPassword":"`+securityPassword+`","email":"renamed@example.test","newPassword":"replacement administration password"}`, "192.0.2.1:1", "")
	if changed.Code != 200 || !strings.Contains(changed.Body.String(), "renamed@example.test") {
		t.Fatalf("change status=%d body=%s", changed.Code, changed.Body.String())
	}
	if got := request(http.MethodGet, profile, "", "192.0.2.1:1", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("session after password change status=%d", got.Code)
	}
	if _, err := auth.Login(context.Background(), "renamed@example.test", "replacement administration password", "192.0.2.1"); err != nil {
		t.Fatalf("login with the new credentials: %v", err)
	}
}

func TestAdministratorProfileCurrentPasswordIsRateLimited(t *testing.T) {
	pool := newSecurityDatabase(t)
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "profile-limit-token", time.Hour, nil)
	if err := auth.ConfigureRatePolicy(adminauth.RatePolicy{AccountMaxFailures: 5, NetworkMaxFailures: 2, Window: time.Hour, Block: time.Hour}); err != nil {
		t.Fatal(err)
	}
	session, err := auth.Setup(context.Background(), "profile-limit-token", "owner@example.test", securityPassword, "profile-setup")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})
	patch := func(current string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "http://pgbase.test/api/admin/profile", strings.NewReader(`{"currentPassword":"`+current+`","newPassword":"replacement administration password"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://pgbase.test")
		r.Header.Set("X-PgBase-CSRF", session.CSRFToken)
		r.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for range 2 {
		if got := patch("incorrect battery staple"); got.Code != http.StatusForbidden {
			t.Fatalf("wrong current password status=%d body=%s", got.Code, got.Body.String())
		}
	}
	got := patch(securityPassword)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" || !strings.Contains(got.Body.String(), `"rate_limited"`) {
		t.Fatalf("limited status=%d body=%s", got.Code, got.Body.String())
	}
	if _, err := auth.Authenticate(context.Background(), session.RawToken); err != nil {
		t.Fatalf("rate-limited attempt ended the session: %v", err)
	}
}
