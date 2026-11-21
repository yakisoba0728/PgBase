package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

// addressAdminAuth implements the combined session-and-allowlist check. Its
// AuthorizeIP must never run once the combined check is available.
type addressAdminAuth struct {
	fakeAdminAuth
	addressErr       error
	addressCalls     int
	address          string
	addressValid     bool
	authorizeIPCalls int
}

func (f *addressAdminAuth) AuthenticateFromAddress(ctx context.Context, rawToken, address string, valid bool) (adminauth.Principal, error) {
	f.addressCalls++
	f.address, f.addressValid = address, valid
	principal, err := f.Authenticate(ctx, rawToken)
	if err != nil {
		return adminauth.Principal{}, err
	}
	if f.addressErr != nil {
		return adminauth.Principal{}, f.addressErr
	}
	return principal, nil
}

func (f *addressAdminAuth) AuthorizeIP(context.Context, uuid.UUID, string) error {
	f.authorizeIPCalls++
	return nil
}

func TestAdminSessionMiddlewareUsesCombinedAddressAuthentication(t *testing.T) {
	session := validSession()
	tests := []struct {
		name       string
		authErr    error
		addressErr error
		status     int
		code       string
	}{
		{name: "allowed", status: http.StatusNoContent},
		{name: "address rejected", addressErr: adminauth.ErrIPRestricted, status: http.StatusUnauthorized, code: "authentication_required"},
		{name: "invalid session", authErr: adminauth.ErrInvalidCredentials, status: http.StatusUnauthorized, code: "authentication_required"},
		{name: "rate limited", authErr: adminauth.ErrRateLimited, status: http.StatusTooManyRequests, code: "rate_limited"},
		{name: "database unavailable", authErr: &pgconn.PgError{Code: "53300", Message: "too many connections"}, status: http.StatusServiceUnavailable, code: "authentication_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := &addressAdminAuth{fakeAdminAuth: fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateErr: test.authErr}, addressErr: test.addressErr}
			nextCalled := false
			handler := authenticateAdminMiddleware(auth, mustURL("https://pgbase.example"), nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				if principal, ok := AdminPrincipalFromContext(r.Context()); !ok || principal.Administrator != session.Administrator {
					t.Fatalf("principal = %#v, %t", principal, ok)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "https://pgbase.example/api/admin/session", nil)
			request.RemoteAddr = "192.0.2.7:1234"
			request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.code != "" {
				assertErrorCode(t, response.Body.Bytes(), test.code)
			}
			if nextCalled != (test.status == http.StatusNoContent) {
				t.Fatalf("next called = %t", nextCalled)
			}
			if auth.addressCalls != 1 || auth.authenticateCalls != 1 || auth.authorizeIPCalls != 0 {
				t.Fatalf("combined=%d authenticate=%d authorizeIP=%d", auth.addressCalls, auth.authenticateCalls, auth.authorizeIPCalls)
			}
			if auth.address != "192.0.2.7" || !auth.addressValid {
				t.Fatalf("address = %q valid=%t", auth.address, auth.addressValid)
			}
		})
	}
}
