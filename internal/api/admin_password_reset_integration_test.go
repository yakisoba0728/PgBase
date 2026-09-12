//go:build integration

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type resetAPIClock struct{ now time.Time }

func (c *resetAPIClock) Now() time.Time { return c.now }

type resetAPIMailer struct {
	messages []mailer.Message
	err      error
}

func (m *resetAPIMailer) Send(_ context.Context, message mailer.Message) error {
	m.messages = append(m.messages, message)
	return m.err
}

func TestAdministratorPasswordResetHTTPIntegration(t *testing.T) {
	pool := newSecurityDatabase(t)
	clock := &resetAPIClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "admin-reset-api-setup-token-0123456789", time.Hour, clock)
	mail := &resetAPIMailer{}
	if err := auth.ConfigurePasswordReset(mail, "http://localhost", time.Minute); err != nil {
		t.Fatal(err)
	}
	session, err := auth.Setup(context.Background(), "admin-reset-api-setup-token-0123456789", "admin@example.test", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})

	for _, address := range []string{"unknown@example.test", "admin@example.test"} {
		request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/request-password-reset", `{"email":"`+address+`"}`)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("reset %s status=%d", address, response.Code)
		}
	}
	if len(mail.messages) != 1 {
		t.Fatalf("mail messages=%d, want 1", len(mail.messages))
	}
	parsed, err := url.Parse(mail.messages[0].Text)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(parsed.Fragment, "token=")
	confirm := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/confirm-password-reset", `{"token":"`+token+`","newPassword":"new correct horse battery staple"}`)
	confirmed := httptest.NewRecorder()
	handler.ServeHTTP(confirmed, confirm)
	if confirmed.Code != http.StatusNoContent {
		t.Fatalf("confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	if _, err := auth.Authenticate(context.Background(), session.RawToken); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("old session error=%v", err)
	}
	reusedRequest := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/confirm-password-reset", `{"token":"`+token+`","newPassword":"new correct horse battery staple"}`)
	reused := httptest.NewRecorder()
	handler.ServeHTTP(reused, reusedRequest)
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused status=%d", reused.Code)
	}

	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/request-password-reset", `{"email":"admin@example.test"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	expiredURL, err := url.Parse(mail.messages[len(mail.messages)-1].Text)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	expired := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/confirm-password-reset", `{"token":"`+strings.TrimPrefix(expiredURL.Fragment, "token=")+`","newPassword":"another correct horse battery staple"}`)
	expiredResponse := httptest.NewRecorder()
	handler.ServeHTTP(expiredResponse, expired)
	if expiredResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d", expiredResponse.Code)
	}
	mail.err = errors.New("SMTP unavailable")
	smtpFailureRequest := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/request-password-reset", `{"email":"admin@example.test"}`)
	smtpFailure := httptest.NewRecorder()
	handler.ServeHTTP(smtpFailure, smtpFailureRequest)
	if smtpFailure.Code != http.StatusNoContent {
		t.Fatalf("SMTP failure status=%d", smtpFailure.Code)
	}
}

func TestAdministratorEmailOTPHTTPCompletionIntegration(t *testing.T) {
	pool := newSecurityDatabase(t)
	clock := &resetAPIClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "admin-email-otp-api-setup-token-012345", time.Hour, clock)
	if err := auth.ConfigureEmailOTP(&resetAPIMailer{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Setup(context.Background(), "admin-email-otp-api-setup-token-012345", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	issued, err := auth.IssueEmailOTP(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/otp/complete", `{"otpId":"`+issued.ID+`","code":"`+issued.Code+`"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	reused := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/otp/complete", `{"otpId":"`+issued.ID+`","code":"`+issued.Code+`"}`)
	reusedResponse := httptest.NewRecorder()
	handler.ServeHTTP(reusedResponse, reused)
	if reusedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("reused status=%d", reusedResponse.Code)
	}
}
