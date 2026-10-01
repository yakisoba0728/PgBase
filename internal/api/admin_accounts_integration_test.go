//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAdministratorAccountHTTPManagement(t *testing.T) {
	pool := newSecurityDatabase(t)
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "account-setup-token", time.Hour, nil)
	session, err := auth.Setup(context.Background(), "account-setup-token", "owner@example.test", securityPassword, "account-setup")
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("http://pgbase.test")})
	request := func(method, path, body string, authenticated, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://pgbase.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://pgbase.test")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
		}
		if csrf {
			r.Header.Set("X-PgBase-CSRF", session.CSRFToken)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const path = "/api/admin/administrators"
	created := request(http.MethodPost, path, `{"email":"second@example.test","password":"second administration password"}`, true, true)
	if created.Code != http.StatusCreated {
		t.Fatalf("create administrator status=%d body=%s", created.Code, created.Body.String())
	}
	var second administratorResponse
	if err := json.Unmarshal(created.Body.Bytes(), &second); err != nil || second.Email != "second@example.test" || !second.Active {
		t.Fatalf("created account=%#v err=%v", second, err)
	}
	for _, secret := range []string{"password", "token", "hash", "second administration password"} {
		if strings.Contains(created.Body.String(), secret) {
			t.Fatalf("creation leaked %q", secret)
		}
	}
	listed := request(http.MethodGet, path+"?limit=1", "", true, false)
	var page struct {
		Items     []administratorResponse `json:"items"`
		NextAfter string                  `json:"nextAfter"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); listed.Code != 200 || err != nil || len(page.Items) != 1 || page.NextAfter == "" {
		t.Fatalf("list first page=%s err=%v", listed.Body.String(), err)
	}
	next := request(http.MethodGet, path+"?limit=1&after="+page.NextAfter, "", true, false)
	var nextPage struct {
		Items     []administratorResponse `json:"items"`
		NextAfter string                  `json:"nextAfter"`
	}
	if err := json.Unmarshal(next.Body.Bytes(), &nextPage); next.Code != 200 || err != nil || len(nextPage.Items) != 1 || nextPage.NextAfter != "" || nextPage.Items[0].ID == page.Items[0].ID {
		t.Fatalf("list next page=%s err=%v", next.Body.String(), err)
	}
	self := request(http.MethodPatch, path+"/"+session.Administrator.ID.String(), `{"active":false}`, true, true)
	if self.Code != http.StatusConflict {
		t.Fatalf("self deactivate=%d %s", self.Code, self.Body.String())
	}
	secondSession, err := auth.Login(context.Background(), second.Email, "second administration password", "second-login")
	if err != nil {
		t.Fatal(err)
	}
	deactivated := request(http.MethodPatch, path+"/"+second.ID.String(), `{"active":false}`, true, true)
	if deactivated.Code != 200 {
		t.Fatalf("deactivate=%d %s", deactivated.Code, deactivated.Body.String())
	}
	if _, err := auth.Authenticate(context.Background(), secondSession.RawToken); err == nil {
		t.Fatal("deactivation retained the target session")
	}
	if got := request(http.MethodPost, path+"/"+second.ID.String()+"/password-reset", `{"newPassword":"replacement administration password"}`, true, true); got.Code != 204 {
		t.Fatalf("password reset=%d %s", got.Code, got.Body.String())
	}
	if _, err := auth.Login(context.Background(), second.Email, "replacement administration password", "inactive-login"); err == nil {
		t.Fatal("password reset activated the account")
	}
	if got := request(http.MethodPatch, path+"/"+second.ID.String(), `{"active":true}`, true, true); got.Code != 200 {
		t.Fatalf("reactivation=%d %s", got.Code, got.Body.String())
	}
	secondSession, err = auth.Login(context.Background(), second.Email, "replacement administration password", "replacement-login")
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, path+"/"+second.ID.String()+"/revoke-sessions", `{}`, true, true); got.Code != 204 {
		t.Fatalf("revoke=%d %s", got.Code, got.Body.String())
	}
	if _, err := auth.Authenticate(context.Background(), secondSession.RawToken); err == nil {
		t.Fatal("explicit revoke retained the target session")
	}
	for _, tc := range []struct {
		method, path, body string
		auth, csrf         bool
		status             int
	}{
		{http.MethodGet, path, "", false, false, 401},
		{http.MethodPost, path, `{"email":"third@example.test","password":"third administration password"}`, true, false, 403},
		{http.MethodPost, path, `{"email":"second@example.test","password":"duplicate administration password"}`, true, true, 409},
		{http.MethodPatch, path + "/" + second.ID.String(), `{"active":true,"email":"override@example.test"}`, true, true, 400},
		{http.MethodGet, path + "?limit=101", "", true, false, 400},
		{http.MethodGet, path + "?after=invalid", "", true, false, 400},
		{http.MethodPatch, path + "/00000000-0000-4000-8000-000000000001", `{"active":true}`, true, true, 404},
	} {
		if got := request(tc.method, tc.path, tc.body, tc.auth, tc.csrf); got.Code != tc.status {
			t.Errorf("%s %s = %d want %d: %s", tc.method, tc.path, got.Code, tc.status, got.Body.String())
		}
	}
}
