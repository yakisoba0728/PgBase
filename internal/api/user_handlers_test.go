package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type stubUserAuth struct {
	calls int
	err   error
}

func (s *stubUserAuth) Register(context.Context, string, string, string) (userauth.Session, error) {
	s.calls++
	return userauth.Session{}, s.err
}
func (s *stubUserAuth) Login(context.Context, string, string, string) (userauth.Session, error) {
	s.calls++
	return userauth.Session{}, s.err
}
func (s *stubUserAuth) Authenticate(context.Context, string) (userauth.Principal, error) {
	s.calls++
	return userauth.Principal{User: userauth.User{ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000101"), Email: "user@example.test"}, ExpiresAt: time.Now().Add(time.Hour)}, s.err
}
func (s *stubUserAuth) Logout(context.Context, string) error { s.calls++; return s.err }

type stubRecordAccess struct{}

func (stubRecordAccess) Get(context.Context, string) (recordaccess.Policy, error) {
	return recordaccess.Policy{}, nil
}
func (stubRecordAccess) Set(context.Context, string, bool) (recordaccess.Policy, error) {
	return recordaccess.Policy{}, nil
}

func TestPublicUserBoundaryRejectsAmbiguousOrCookieOnlyCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		cookie  bool
		want    int
		calls   int
	}{
		{name: "missing", want: 401},
		{name: "admin cookie", cookie: true, want: 401},
		{name: "basic", headers: []string{"Basic opaque"}, want: 401},
		{name: "missing token", headers: []string{"Bearer"}, want: 401},
		{name: "multiple headers", headers: []string{"Bearer first", "Bearer second"}, want: 401},
		{name: "oversize", headers: []string{"Bearer " + strings.Repeat("a", 257)}, want: 401},
		{name: "valid bearer", headers: []string{"Bearer opaque"}, want: 200, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &stubUserAuth{}
			router, err := NewRouter(completeDependencies(Dependencies{UserAuth: auth, RecordAccess: stubRecordAccess{}}))
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
			for _, v := range tc.headers {
				r.Header.Add("Authorization", v)
			}
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: adminDevelopmentCookie, Value: "admin-session"})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want || auth.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, auth.calls, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("authentication response can be cached")
			}
			if strings.Contains(w.Body.String(), "opaque") {
				t.Fatal("session lookup echoed bearer token")
			}
		})
	}
}

func TestPublicRegistrationIsExplicitlyEnabledAndStrictlyDecoded(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		enabled     bool
		want, calls int
	}{
		{name: "disabled", body: `{"email":"user@example.test","password":"test-password"}`, want: 403},
		{name: "unknown field", enabled: true, body: `{"email":"user@example.test","password":"test-password","role":"administrator"}`, want: 400},
		{name: "enabled calls service", enabled: true, body: `{"email":"user@example.test","password":"test-password"}`, want: 201, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &stubUserAuth{}
			router, err := NewRouter(completeDependencies(Dependencies{UserAuth: auth, RecordAccess: stubRecordAccess{}, UserRegistrationEnabled: tc.enabled}))
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want || auth.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, auth.calls, w.Body.String())
			}
		})
	}
}

func TestUserAuthFailuresAreRedactedAndClassified(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{userauth.ErrInvalidCredentials, 401, "invalid_credentials"},
		{userauth.ErrRateLimited, 429, "rate_limited"},
		{errors.New("database failed with secret-sentinel"), 503, "authentication_unavailable"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
		writeUserAuthError(w, r, tc.err)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d", w.Code, tc.status)
		}
		assertErrorCode(t, w.Body.Bytes(), tc.code)
		if strings.Contains(w.Body.String(), "secret-sentinel") {
			t.Fatal("internal error leaked")
		}
	}
}

func TestPublicRoutesPreserveAdmissionAndBudgetContracts(t *testing.T) {
	d := completeDependencies(Dependencies{UserAuth: &stubUserAuth{}, RecordAccess: stubRecordAccess{}, AdminUI: http.NewServeMux(), MetricsExposition: http.NewServeMux()})
	routes := declareRoutes(d, true)
	if err := validateRoutes(routes); err != nil {
		t.Fatal(err)
	}
	counts := map[accessPolicy]int{}
	for _, r := range routes {
		if strings.HasPrefix(r.pattern, "/api/v1/user/") || strings.HasPrefix(r.pattern, "/api/v1/auth/") {
			counts[r.access]++
		}
	}
	if counts[accessUserSession] != 9 || counts[accessPublic] != 2 {
		t.Fatalf("user route admissions=%v", counts)
	}
	d.RecordAccess = nil
	if _, err := NewRouter(d); err == nil {
		t.Fatal("incomplete user capability wiring accepted")
	}
}
