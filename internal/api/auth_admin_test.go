package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type fakeAuthAdministration struct {
	AuthAdministration
	value userauth.AuthAdministration
}

func (f *fakeAuthAdministration) GetAuthAdministration(context.Context, uuid.UUID) (userauth.AuthAdministration, error) {
	return f.value, nil
}

func TestAuthAdministrationRoutesRequireAdminSession(t *testing.T) {
	collectionID := uuid.MustParse("f3178843-0f03-4108-b110-ae7db8c8738c")
	handler := mustRouter(t, Dependencies{AuthAdministration: &fakeAuthAdministration{}})
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/admin/auth-collections/"+collectionID.String()+"/administration", nil),
		httptest.NewRequest(http.MethodPut, "/api/admin/auth-collections/"+collectionID.String()+"/policy", strings.NewReader(`{"passwordEnabled":true,"minimumPasswordBytes":12,"mfaEnabled":false}`)),
	}
	for _, request := range requests {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.com")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", request.Method, response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "authentication_required")
	}
}

func TestAuthAdministrationDefaultCollectionsSerializeAsArrays(t *testing.T) {
	collectionID := uuid.MustParse("f3178843-0f03-4108-b110-ae7db8c8738c")
	session := validSession()
	auth := &fakeAdminAuth{
		authenticateAdmin:  session.Administrator,
		authenticateExpiry: time.Now().Add(time.Hour),
	}
	service := &fakeAuthAdministration{value: userauth.AuthAdministration{
		Policy:    userauth.AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 12},
		Providers: []userauth.OAuthProviderConfig{},
		Templates: []userauth.MailTemplate{},
	}}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, AuthAdministration: service, PublicURL: mustURL("https://pgbase.example")})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/auth-collections/"+collectionID.String()+"/administration", nil)
	request.Header.Set("Origin", "https://pgbase.example")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["providers"]) != "[]" || string(body["templates"]) != "[]" {
		t.Fatalf("providers=%s templates=%s body=%s", body["providers"], body["templates"], response.Body.String())
	}
}
