package api

import (
	"context"
	"net/url"
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

func (f *fakeAdminAuth) Authenticate(_ context.Context, rawToken string) (adminauth.Administrator, time.Time, error) {
	f.authenticateCalls++
	f.authenticateToken = rawToken
	return f.authenticateAdmin, f.authenticateExpiry, f.authenticateErr
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
		RawToken:      "fixed-session-token",
		ExpiresAt:     createdAt.Add(12 * time.Hour),
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
		AdminAuth:   auth,
		PublicURL:   publicURL,
		TokenSecret: testTokenSecret,
	}
}
