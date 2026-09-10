//go:build integration

package pgbase

import (
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func TestEmailOTPDefaultAndNamedRealmThroughHTTPAndSMTP(t *testing.T) {
	address, messages := localMailSink(t)
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.SMTP = SMTPOptions{Address: address, From: "local@example.com", AllowInsecure: true}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	namedID := uuid.New()
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/admin/auth-collections", map[string]any{"id": namedID, "name": "otp_customers", "registrationEnabled": true, "profileSchema": map[string]string{}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	for _, realm := range []struct {
		id   uuid.UUID
		path string
	}{
		{userauth.DefaultCollectionID, "/api/v1/auth"},
		{namedID, "/api/v1/auth/otp_customers"},
	} {
		t.Run(realm.path, func(t *testing.T) {
			policyPath := "/api/admin/auth-collections/" + realm.id.String() + "/policy"
			policy := map[string]any{"passwordEnabled": true, "minimumPasswordBytes": 12, "mfaEnabled": true, "otpEnabled": true, "otpDurationSeconds": 60, "otpMaxAttempts": 3}
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", policyPath, policy, "", cookie, nil), 403)
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", policyPath, policy, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
			registered := publicUsersRequest(t, fixture, "POST", realm.path+"/register", map[string]any{"email": "otp-http@example.test", "password": publicUserOnePass}, "", nil, nil)
			assertPublicUsersStatus(t, registered, 201)
			var initial publicUserSession
			decodePublicUsersJSON(t, registered, &initial)
			requested := publicUsersRequest(t, fixture, "POST", realm.path+"/otp/request", map[string]any{"email": initial.User.Email}, "", nil, nil)
			assertPublicUsersStatus(t, requested, 200)
			var challenge userauth.EmailOTPRequest
			decodePublicUsersJSON(t, requested, &challenge)
			if challenge.OTPID == "" || !challenge.ExpiresAt.After(time.Now()) {
				t.Fatal("missing OTP request receipt")
			}
			var code string
			select {
			case mail := <-messages:
				match := regexp.MustCompile(`one-time login code is: ([0-9]{6})`).FindStringSubmatch(mail)
				if len(match) != 2 {
					t.Fatal("local SMTP did not receive a usable OTP")
				}
				code = match[1]
			case <-time.After(5 * time.Second):
				t.Fatal("local SMTP did not receive OTP mail")
			}
			wrongPath := "/api/v1/auth/otp_customers"
			if realm.id == namedID {
				wrongPath = "/api/v1/auth"
			}
			body := map[string]any{"otpId": challenge.OTPID, "code": code}
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", wrongPath+"/otp/complete", body, "", nil, nil), 401)
			completed := publicUsersRequest(t, fixture, "POST", realm.path+"/otp/complete", body, "", nil, nil)
			assertPublicUsersStatus(t, completed, 200)
			var session publicUserSession
			decodePublicUsersJSON(t, completed, &session)
			if session.User.ID != initial.User.ID || session.Token == "" {
				t.Fatal("OTP issued a session for the wrong identity")
			}
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", realm.path+"/session", nil, session.Token, nil, nil), 200)
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", realm.path+"/otp/complete", body, "", nil, nil), 401)
			policy["otpEnabled"] = false
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", policyPath, policy, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, realm.path+"/session", nil, session.Token, nil, nil), 401)
		})
	}
}
