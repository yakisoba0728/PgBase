package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type fakeAuthAdministration struct {
	AuthAdministration
	value userauth.AuthAdministration
}

func (f *fakeAuthAdministration) GetAuthAdministration(context.Context, uuid.UUID) (userauth.AuthAdministration, error) {
	return f.value, nil
}

func (f *fakeAuthAdministration) PutMailTemplate(_ context.Context, _ uuid.UUID, t userauth.MailTemplate) (userauth.MailTemplate, error) {
	switch t.ETag {
	case `"stale"`:
		return t, userauth.ErrPreconditionFailed
	case `"current"`:
		t.ETag = `"next"`
		return t, nil
	}
	return t, userauth.ErrValidation
}

func (f *fakeAuthAdministration) PutTokenPolicy(context.Context, uuid.UUID, userauth.TokenPolicy) (userauth.TokenPolicy, error) {
	return userauth.TokenPolicy{}, errors.Join(userauth.ErrValidation, &schema.ValidationError{Details: []schema.ValidationDetail{{Field: "ttlSeconds", Code: "invalid"}}})
}

func TestAuthAdministrationConditionalWritesAndFieldDetails(t *testing.T) {
	collectionID := uuid.MustParse("f3178843-0f03-4108-b110-ae7db8c8738c")
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: time.Now().Add(time.Hour)}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, AuthAdministration: &fakeAuthAdministration{}, PublicURL: mustURL("https://pgbase.example")})
	send := func(path, ifMatch, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/admin/auth-collections/"+collectionID.String()+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://pgbase.example")
		request.Header.Set("X-PgBase-CSRF", session.CSRFToken)
		if ifMatch != "" {
			request.Header.Set("If-Match", ifMatch)
		}
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	template := `{"subject":"s","body":"b {{action_url}}"}`
	if response := send("/mail-templates/verify", `"current"`, template); response.Code != http.StatusOK || response.Header().Get("ETag") != `"next"` {
		t.Fatalf("matching write status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if response := send("/mail-templates/verify", `"stale"`, template); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale write status=%d body=%s", response.Code, response.Body.String())
	} else {
		assertErrorCode(t, response.Body.Bytes(), "precondition_failed")
	}
	if response := send("/mail-templates/verify", `"a", "b"`, template); response.Code != http.StatusBadRequest {
		t.Fatalf("tag list status=%d", response.Code)
	}
	response := send("/token-policies/session", "", `{"ttlSeconds":1}`)
	var body struct {
		Error struct {
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Error.Details["ttlSeconds"]) != 1 {
		t.Fatalf("field details status=%d body=%s", response.Code, response.Body.String())
	}
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
