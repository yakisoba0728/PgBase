package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type authCollectionMethodStub struct {
	AuthCollections
	collection userauth.AuthCollection
	methods    userauth.PublicAuthMethods
	methodsErr error
	refreshed  userauth.Session
	refreshErr error
	refreshID  uuid.UUID
	refreshTok string
}

func (s *authCollectionMethodStub) GetAuthCollection(context.Context, string) (userauth.AuthCollection, error) {
	return s.collection, nil
}
func (s *authCollectionMethodStub) PublicAuthMethods(context.Context, uuid.UUID) (userauth.PublicAuthMethods, error) {
	return s.methods, s.methodsErr
}
func (s *authCollectionMethodStub) RefreshSessionForCollection(_ context.Context, id uuid.UUID, token string) (userauth.Session, error) {
	s.refreshID, s.refreshTok = id, token
	return s.refreshed, s.refreshErr
}

func TestCollectionAuthMethodsExposeOnlyPublicCapabilities(t *testing.T) {
	collection := userauth.AuthCollection{ID: uuid.MustParse("019c5b18-8b85-7c34-8d14-3f9aaf8484b1"), Name: "members"}
	service := &authCollectionMethodStub{collection: collection, methods: userauth.PublicAuthMethods{PasswordEnabled: true, OTPEnabled: true, MFAEnabled: true, OAuthProviders: []string{"github", "oidc"}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/members/methods", nil)
	authCollectionEndpoints{dependencies: Dependencies{AuthCollections: service}}.methods(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["passwordEnabled"] != true || body["otpEnabled"] != true || body["mfaEnabled"] != true {
		t.Fatalf("capabilities=%v", body)
	}
	providers, ok := body["oauthProviders"].([]any)
	if !ok || len(providers) != 2 || providers[0] != "github" {
		t.Fatalf("providers=%#v", body["oauthProviders"])
	}
	for _, forbidden := range []string{"clientId", "clientSecret", "authorizationUrl", "tokenUrl", "redirectUris"} {
		if _, found := body[forbidden]; found {
			t.Fatalf("public methods leaked %s: %v", forbidden, body)
		}
	}
}

func TestCollectionAuthRefreshUsesCurrentRealmAndReturnsReplacement(t *testing.T) {
	collection := userauth.AuthCollection{ID: uuid.MustParse("019c5b18-8b85-7c34-8d14-3f9aaf8484b1"), Name: "members"}
	user := userauth.User{ID: uuid.MustParse("019c5b18-8b85-7c34-8d14-3f9aaf8484b2"), AuthCollectionID: collection.ID, Email: "member@example.test", CreatedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)}
	service := &authCollectionMethodStub{collection: collection, refreshed: userauth.Session{User: user, RawToken: "replacement", ExpiresAt: user.CreatedAt.Add(time.Hour)}}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/members/refresh", nil)
	r = r.WithContext(context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: user}, token: "presented"}))
	w := httptest.NewRecorder()
	authCollectionEndpoints{dependencies: Dependencies{AuthCollections: service}}.refresh(w, r)
	if w.Code != http.StatusOK || service.refreshID != collection.ID || service.refreshTok != "presented" {
		t.Fatalf("status=%d id=%s token=%q body=%s", w.Code, service.refreshID, service.refreshTok, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) || !containsJSONValue(t, w.Body.Bytes(), "token", "replacement") {
		t.Fatalf("replacement response=%s", w.Body.String())
	}
}

func containsJSONValue(t *testing.T, body []byte, key, want string) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	got, _ := value[key].(string)
	return got == want
}
