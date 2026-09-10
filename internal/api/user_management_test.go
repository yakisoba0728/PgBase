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
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func (s *stubUserAuth) ListUsers(context.Context, uuid.UUID, int) (userauth.UserPage, error) {
	s.calls++
	return userauth.UserPage{}, s.err
}
func (s *stubUserAuth) SetUserActive(_ context.Context, id uuid.UUID, active bool) (userauth.ManagedUser, error) {
	s.calls++
	return userauth.ManagedUser{User: userauth.User{ID: id}, Active: active}, s.err
}
func (s *stubUserAuth) RevokeUserSessions(context.Context, uuid.UUID) (int64, error) {
	s.calls++
	return 2, s.err
}
func (s *stubUserAuth) ChangePassword(context.Context, uuid.UUID, string, string) error {
	s.calls++
	return s.err
}
func (s *stubUserAuth) Impersonate(_ context.Context, id uuid.UUID, _ string, duration time.Duration) (userauth.Session, error) {
	s.calls++
	return userauth.Session{RawToken: "temporary", User: userauth.User{ID: id, AuthCollectionID: userauth.DefaultCollectionID}, ExpiresAt: time.Now().Add(duration), Impersonated: true, ImpersonatingAdministratorID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000099")}, s.err
}

func TestImpersonationHandlerReturnsMarkedBoundedSession(t *testing.T) {
	auth := &stubUserAuth{}
	id := uuid.New()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+id.String()+"/impersonate", strings.NewReader(`{"durationSeconds":120}`))
	r.SetPathValue("id", id.String())
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, AdminPrincipal{RawSession: strings.Repeat("a", 43)}))
	w := httptest.NewRecorder()
	userEndpoints{Dependencies{UserAuth: auth}}.impersonateUser(w, r)
	if w.Code != http.StatusOK || auth.calls != 1 || !strings.Contains(w.Body.String(), `"impersonated":true`) || !strings.Contains(w.Body.String(), `"impersonatingAdministratorId"`) {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, auth.calls, w.Body.String())
	}
}

func TestUserPageInputRejectsAmbiguousAndUnboundedRequests(t *testing.T) {
	for _, raw := range []string{"limit=0", "limit=101", "limit=x", "limit=1&limit=2", "after=", "after=wrong", "after=00000000-0000-0000-0000-000000000000", "sort=email", "after=%", "limit=", "limit=1;after=x"} {
		auth := &stubUserAuth{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/admin/users", nil)
		r.URL.RawQuery = raw
		userEndpoints{Dependencies{UserAuth: auth}}.listUsers(w, r)
		if w.Code != http.StatusBadRequest || auth.calls != 0 {
			t.Errorf("query %q: status=%d calls=%d", raw, w.Code, auth.calls)
		}
	}
	after, limit, err := userPageParameters("")
	if err != nil || after != uuid.Nil || limit != 50 {
		t.Fatalf("defaults = %s %d %v", after, limit, err)
	}
	id := uuid.New()
	after, limit, err = userPageParameters("after=" + id.String() + "&limit=100")
	if err != nil || after != id || limit != 100 {
		t.Fatalf("bounded page = %s %d %v", after, limit, err)
	}
}

func TestUserManagementStrictInputsAndResponseShapes(t *testing.T) {
	id := uuid.New().String()
	for _, tc := range []struct {
		name, id, body string
		status, calls  int
	}{
		{"empty", id, `{}`, 400, 0},
		{"null active", id, `{"active":null}`, 400, 0},
		{"wrong type", id, `{"active":"false"}`, 400, 0},
		{"unknown field", id, `{"active":false,"password":"secret"}`, 400, 0},
		{"bad ID", "invalid", `{"active":false}`, 400, 0},
		{"nil ID", uuid.Nil.String(), `{"active":false}`, 400, 0},
		{"explicit false", id, `{"active":false}`, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &stubUserAuth{}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("PATCH", "/api/admin/users/"+tc.id, strings.NewReader(tc.body))
			r.SetPathValue("id", tc.id)
			r.Header.Set("Content-Type", "application/json")
			userEndpoints{Dependencies{UserAuth: auth}}.setUserActive(w, r)
			if w.Code != tc.status || auth.calls != tc.calls || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, auth.calls, w.Body.String())
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), `"active":false`) {
				t.Fatal("explicit false was lost")
			}
		})
	}
	w := httptest.NewRecorder()
	userEndpoints{Dependencies{UserAuth: &stubUserAuth{}}}.listUsers(w, httptest.NewRequest("GET", "/api/admin/users", nil))
	if strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty page = %s", w.Body.String())
	}
}

func TestUserManagementFailureClassificationAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{userauth.ErrValidation, 400, "invalid_request"},
		{userauth.ErrNotFound, 404, "not_found"},
		{errors.New("database secret sentinel"), 503, "user_management_unavailable"},
	} {
		w := httptest.NewRecorder()
		writeUserManagementError(w, httptest.NewRequest("GET", "/api/admin/users", nil), tc.err)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "sentinel") {
			t.Fatalf("error status=%d body=%s", w.Code, w.Body.String())
		}
		assertErrorCode(t, w.Body.Bytes(), tc.code)
	}
}

func TestUserLifecycleRouteAdmissionsAndPasswordBodyBound(t *testing.T) {
	for _, r := range (userEndpoints{}).lifecycleRoutes() {
		want := accessAdminSession
		if r.method == "GET" {
			want = accessAdminSameOriginRead
		}
		if r.pattern == "/api/v1/auth/change-password" {
			want = accessUserSession
			if r.maximumRequestBytes != maximumCredentialRequestBytes {
				t.Fatal("password change lacks credential body bound")
			}
		}
		if r.access != want {
			t.Fatalf("%s %s access=%s", r.method, r.pattern, r.access)
		}
	}
	for _, body := range []string{`{}`, `{"currentPassword":"old","newPassword":"new","userId":"other"}`, `{"currentPassword":"old","newPassword":""}`} {
		auth := &stubUserAuth{}
		router, err := NewRouter(completeDependencies(Dependencies{UserAuth: auth, RecordAccess: stubRecordAccess{}}))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/api/v1/auth/change-password", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 400 || auth.calls != 1 {
			t.Fatalf("password input: status=%d calls=%d", w.Code, auth.calls)
		}
	}
}
