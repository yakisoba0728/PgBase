package api

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
	"github.com/yakisoba0728/PgBase/internal/userauth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticatedStreamCachePolicyAtCommit(t *testing.T) {
	for _, mode := range []string{"header", "write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			w := httptest.NewRecorder()
			noStoreCustomHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "public, max-age=2592000")
				switch mode {
				case "header":
					w.WriteHeader(200)
				case "write":
					_, _ = w.Write([]byte("private"))
				case "flush":
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Fatal(err)
					}
				}
			})).ServeHTTP(w, httptest.NewRequest("GET", "/custom", nil))
			if got := w.Result().Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("committed Cache-Control=%q", got)
			}
		})
	}
}
func TestStrictJSONPreservesProfileNumbers(t *testing.T) {
	var result struct {
		Profile map[string]any `json:"profile"`
	}
	if err := decodeStrictJSONBytes([]byte(`{"profile":{"n":9007199254740993,"fraction":1.234567890123456789}}`), &result); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"n": "9007199254740993", "fraction": "1.234567890123456789"} {
		got, ok := result.Profile[key].(json.Number)
		if !ok || got.String() != want {
			t.Fatalf("%s=%#v", key, result.Profile[key])
		}
	}
}
func TestMissingServiceAuthenticatorRejectsCredential(t *testing.T) {
	r := httptest.NewRequest("GET", "/custom", nil)
	r.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()
	serviceSessionMiddleware(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("admitted missing authenticator") })).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestRealtimeRefreshesVerifiedRequestContext(t *testing.T) {
	id := uuid.New()
	original := userauth.Principal{User: userauth.User{ID: id, Verified: true}}
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id, Verified: false}}}
	hub := newRealtimeHub(Dependencies{UserAuth: auth})
	client := &realtimeClient{principal: identity.Subject{Kind: identity.User, ID: id}, token: "live"}
	r := httptest.NewRequest("GET", "/api/realtime", nil)
	r = r.WithContext(context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: original, token: "live"}))
	current, ok := hub.currentRealtimeRequest(r, client)
	if !ok || ruleRequestContext(current).Auth["verified"] != false {
		t.Fatalf("current auth=%v accepted=%v", ruleRequestContext(current).Auth, ok)
	}
	if ruleRequestContext(r).Auth["verified"] != true {
		t.Fatal("mutated original request")
	}
	auth.err = userauth.ErrInvalidCredentials
	if _, ok := hub.currentRealtimeRequest(r, client); ok {
		t.Fatal("revoked user admitted")
	}
}

type verifiedContextRealtime struct {
	sessionRealtimeStub
	observed bool
}

func (s *verifiedContextRealtime) ReadFor(_ context.Context, request realtime.ReadRequest) ([]realtime.Event, int64, error) {
	s.observed = request.RuleContext != nil && request.RuleContext.Auth["verified"] == false
	return nil, 0, recordaccess.ErrDenied
}
func TestRealtimeDeliveryUsesCurrentVerifiedPolicyContext(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id, Verified: false}}}
	reader := &verifiedContextRealtime{}
	hub := newRealtimeHub(Dependencies{UserAuth: auth, Realtime: reader})
	zero := int64(0)
	client := &realtimeClient{principal: identity.Subject{Kind: identity.User, ID: id}, token: "live", subscriptions: []realtimeSubscription{{raw: "notes", collection: "notes", cursor: &zero}}}
	r := httptest.NewRequest("GET", "/api/realtime", nil)
	r = r.WithContext(context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: id, Verified: true}}, token: "live"}))
	w := httptest.NewRecorder()
	_ = hub.deliverRecordMessages(w, r, http.NewResponseController(w), client)
	if !reader.observed {
		t.Fatal("live policy read retained original verified=true")
	}
	if strings.Contains(w.Body.String(), "event: notes") {
		t.Fatalf("protected record delivered: %s", w.Body.String())
	}
}

type changingVerifiedRealtime struct {
	sessionRealtimeStub
	auth *realtimeAuthStub
}

func (s *changingVerifiedRealtime) ReadFor(_ context.Context, request realtime.ReadRequest) ([]realtime.Event, int64, error) {
	s.auth.principal.User.Verified = false
	return []realtime.Event{{Sequence: 1, Collection: "notes", Operation: "create", PublicID: "protected", Record: map[string]any{"id": "protected"}}}, 1, nil
}
func TestRealtimeDiscardsBatchWhenVerifiedChangesDuringRead(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id, Verified: true}}}
	hub := newRealtimeHub(Dependencies{UserAuth: auth, Realtime: &changingVerifiedRealtime{auth: auth}})
	zero := int64(0)
	client := &realtimeClient{principal: identity.Subject{Kind: identity.User, ID: id}, token: "live", subscriptions: []realtimeSubscription{{raw: "notes", collection: "notes", cursor: &zero}}}
	r := httptest.NewRequest("GET", "/api/realtime", nil)
	r = r.WithContext(context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: auth.principal, token: "live"}))
	w := httptest.NewRecorder()
	err := hub.deliverRecordMessages(w, r, http.NewResponseController(w), client)
	if err != recordaccess.ErrDenied || w.Body.Len() != 0 {
		t.Fatalf("stale batch delivered: %s err=%v", w.Body.String(), err)
	}
}

type invalidSubjectServiceAuthenticator struct {
	ServiceTokens
	subject identity.Subject
}

func (s invalidSubjectServiceAuthenticator) Authenticate(context.Context, string) (serviceauth.Principal, error) {
	return serviceauth.Principal{Subject: s.subject}, nil
}
func TestServiceAuthenticatorRejectsInvalidSuccessfulPrincipal(t *testing.T) {
	for _, subject := range []identity.Subject{{Kind: identity.Service}, {Kind: identity.Anonymous}, {Kind: identity.User, ID: uuid.New()}} {
		r := httptest.NewRequest("GET", "/custom", nil)
		r.Header.Set("Authorization", "Bearer invalid")
		w := httptest.NewRecorder()
		serviceSessionMiddleware(invalidSubjectServiceAuthenticator{subject: subject}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid principal admitted") })).ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("status=%d subject=%+v", w.Code, subject)
		}
	}
}
func TestRealtimeInvalidUserBearerWithoutServiceAuthIsUnauthorized(t *testing.T) {
	auth := &realtimeAuthStub{err: userauth.ErrInvalidCredentials}
	d := Dependencies{UserAuth: auth, PublicURL: mustURL("https://admin.example.test")}
	r := httptest.NewRequest("GET", "/api/realtime", nil)
	r.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()
	optionalRealtimeAdmission(d, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid bearer admitted") })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
