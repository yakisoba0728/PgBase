//go:build integration

package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
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

// An administrator with TOTP enabled gets the same otp_required step as
// password login and completes it by resending the request with otpCode.
func TestAdministratorEmailOTPHTTPRequiresTOTPWhenEnabled(t *testing.T) {
	pool := newSecurityDatabase(t)
	clock := &resetAPIClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "admin-email-otp-api-setup-token-012345", time.Hour, clock)
	if err := auth.ConfigureEmailOTP(&resetAPIMailer{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Setup(context.Background(), "admin-email-otp-api-setup-token-012345", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	const totpSecret = "JBSWY3DPEHPK3PXP"
	if err := auth.EnableTOTP(context.Background(), "admin@example.test", totpSecret); err != nil {
		t.Fatal(err)
	}
	issued, err := auth.IssueEmailOTP(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})
	complete := func(body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/otp/complete", body))
		return response
	}
	required := complete(`{"otpId":"` + issued.ID + `","code":"` + issued.Code + `"}`)
	if required.Code != http.StatusUnauthorized || !strings.Contains(required.Body.String(), `"otp_required"`) {
		t.Fatalf("without TOTP status=%d body=%s", required.Code, required.Body.String())
	}
	if cookies := required.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("otp_required issued cookies %v", cookies)
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(totpSecret)
	if err != nil {
		t.Fatal(err)
	}
	completed := complete(`{"otpId":"` + issued.ID + `","code":"` + issued.Code + `","otpCode":"` + httpTestTOTP(raw, clock.now) + `"}`)
	if completed.Code != http.StatusOK {
		t.Fatalf("with TOTP status=%d body=%s", completed.Code, completed.Body.String())
	}
}

func httpTestTOTP(secret []byte, now time.Time) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := (int(digest[offset])&127)<<24 | int(digest[offset+1])<<16 | int(digest[offset+2])<<8 | int(digest[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
