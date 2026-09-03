package api

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
)

var testTokenSecret = []byte("0123456789abcdef0123456789abcdef")

type fakeAdminAuth struct {
	setupRequired      bool
	setupRequiredErr   error
	setupRequiredCalls int
	setupSession       adminauth.Session
	setupErr           error
	loginSession       adminauth.Session
	loginErr           error
	authenticateAdmin  adminauth.Administrator
	authenticateExpiry time.Time
	authenticateErr    error
	logoutErr          error
	setupCalls         int
	loginCalls         int
	authenticateCalls  int
	logoutCalls        int
	setupToken         string
	setupEmail         string
	setupPassword      string
	setupSubject       string
	loginEmail         string
	loginPassword      string
	loginSubject       string
	authenticateToken  string
	logoutToken        string
}

func (f *fakeAdminAuth) SetupRequired(context.Context) (bool, error) {
	f.setupRequiredCalls++
	return f.setupRequired, f.setupRequiredErr
}

func (f *fakeAdminAuth) Setup(_ context.Context, setupToken, email, password, subject string) (adminauth.Session, error) {
	f.setupCalls++
	f.setupToken = setupToken
	f.setupEmail = email
	f.setupPassword = password
	f.setupSubject = subject
	return f.setupSession, f.setupErr
}

func (f *fakeAdminAuth) Login(_ context.Context, email, password, subject string) (adminauth.Session, error) {
	f.loginCalls++
	f.loginEmail = email
	f.loginPassword = password
	f.loginSubject = subject
	return f.loginSession, f.loginErr
}

func (f *fakeAdminAuth) Authenticate(_ context.Context, rawToken string) (adminauth.Principal, error) {
	f.authenticateCalls++
	f.authenticateToken = rawToken
	if f.authenticateErr != nil {
		return adminauth.Principal{}, f.authenticateErr
	}
	return adminauth.Principal{
		Administrator: f.authenticateAdmin,
		ExpiresAt:     f.authenticateExpiry,
		CSRFToken:     adminauth.DeriveCSRFToken(testTokenSecret, rawToken),
	}, nil
}

func (f *fakeAdminAuth) Logout(_ context.Context, rawToken string) error {
	f.logoutCalls++
	f.logoutToken = rawToken
	return f.logoutErr
}

func validSession() adminauth.Session {
	createdAt := time.Date(2026, time.August, 28, 3, 4, 5, 0, time.UTC)
	administrator := adminauth.Administrator{
		ID:        uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
		Email:     "admin@example.com",
		Active:    true,
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Minute),
	}
	return adminauth.Session{
		Administrator: administrator,
		RawToken:      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		ExpiresAt:     createdAt.Add(12 * time.Hour),
		// The real service derives this inside adminauth, so the fake has to
		// as well or the transport would be tested against a session shape it
		// can never receive.
		CSRFToken: adminauth.DeriveCSRFToken(testTokenSecret, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func testDependencies(auth AdminAuth, publicURL *url.URL) Dependencies {
	return Dependencies{
		AdminAuth: auth,
		PublicURL: publicURL,
	}
}

// stubReadiness satisfies the readiness dependency for router construction in
// handler tests that do not exercise the health routes.
type stubReadiness struct{}

func (stubReadiness) Ready(context.Context) error { return nil }

// completeDependencies fills every module the route registry requires, leaving
// the caller's choices intact. Construction now rejects a partially wired
// router, so a handler test states the modules it cares about and inherits
// inert stubs for the rest instead of relying on request-time nil checks.
func completeDependencies(dependencies Dependencies) Dependencies {
	if dependencies.SchemaApplyTimeout == 0 {
		dependencies.SchemaApplyTimeout = 10 * time.Minute
	}
	if dependencies.Readiness == nil {
		dependencies.Readiness = stubReadiness{}
	}
	if dependencies.AdminAuth == nil {
		dependencies.AdminAuth = &fakeAdminAuth{}
	}
	if dependencies.Schema == nil {
		dependencies.Schema = &fakeSchemaService{}
	}
	if dependencies.Records == nil {
		dependencies.Records = &fakeRecordService{}
	}
	if dependencies.System == nil {
		dependencies.System = fakeSystemStatus{}
	}
	if dependencies.SchemaHealth == nil {
		dependencies.SchemaHealth = fakeSchemaHealth{}
	}
	return dependencies
}

// deadlineWriter makes a test response writer behave like the real server
// connection, which always supports a write deadline. Without it a route
// carrying a deadline policy would fail closed in every unit test for a
// reason no deployed server has.
type deadlineWriter struct {
	http.ResponseWriter
	writeDeadline time.Time
}

func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return nil
}

func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func mustRouter(tb testing.TB, dependencies Dependencies) http.Handler {
	tb.Helper()
	handler, err := NewRouter(completeDependencies(dependencies))
	if err != nil {
		tb.Fatalf("construct router: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&deadlineWriter{ResponseWriter: w}, r)
	})
}
