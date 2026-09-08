//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

const (
	publicUsersOrigin = "https://pgbase.test"
	publicSetupToken  = "public-users-setup-token-0123456789abcdef"
	publicAdminEmail  = "owner-admin@example.test"
	publicAdminPass   = "admin-fixture-passphrase-8164"
	publicUserOnePass = "user-one-fixture-passphrase-7281"
	publicUserTwoPass = "user-two-fixture-passphrase-4936"
)

type publicUserSession struct {
	Token string `json:"token"`
	User  struct {
		ID        uuid.UUID `json:"id"`
		Email     string    `json:"email"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"user"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type publicAdminSession struct {
	CSRFToken string `json:"csrfToken"`
}

type publicUsersFixture struct {
	app    *App
	server *httptest.Server
}

func TestPublicUserRegistrationIsOptIn(t *testing.T) {
	app := newPublicUsersApp(t, false)
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register",
		map[string]any{"email": "disabled@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, response, http.StatusForbidden)
}

func TestPublicUsersOwnOnlyRecordHTTPContract(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "notes")

	first := registerPublicUser(t, app, "first-user@example.test", publicUserOnePass)
	second := registerPublicUser(t, app, "second-user@example.test", publicUserTwoPass)
	if first.User.ID == uuid.Nil || second.User.ID == uuid.Nil || first.User.ID == second.User.ID ||
		first.Token == "" || second.Token == "" || first.ExpiresAt.IsZero() || second.ExpiresAt.IsZero() ||
		first.User.CreatedAt.IsZero() || second.User.CreatedAt.IsZero() {
		t.Fatal("registration did not return two complete, distinct sessions")
	}

	collectionPath := "/api/v1/user/collections/notes/records"
	locked := publicUsersRequest(t, app, http.MethodPost, collectionPath,
		map[string]any{"title": "before access"}, first.Token, nil,
		map[string]string{"Idempotency-Key": "locked-record-create-0001"})
	assertPublicUsersStatus(t, locked, http.StatusForbidden)

	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/notes/user-access",
		map[string]any{"enabled": true}, "", adminCookie,
		map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)

	adminOnPublic := publicUsersRequest(t, app, http.MethodGet, collectionPath, nil, "", adminCookie, nil)
	assertPublicUsersStatus(t, adminOnPublic, http.StatusUnauthorized)
	userOnAdmin := publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections", nil, first.Token, nil, nil)
	assertPublicUsersStatus(t, userOnAdmin, http.StatusUnauthorized)

	created := publicUsersRequest(t, app, http.MethodPost, collectionPath,
		map[string]any{"title": "private note"}, first.Token, nil,
		map[string]string{"Idempotency-Key": "owned-record-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	createdETag := created.Header().Get("ETag")
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	recordID, ok := record["id"].(string)
	if !ok || uuid.Validate(recordID) != nil || createdETag == "" {
		t.Fatal("created record did not return an id and ETag")
	}
	recordPath := collectionPath + "/" + recordID

	firstList := publicUsersRequest(t, app, http.MethodGet, collectionPath, nil, first.Token, nil, nil)
	assertPublicUsersStatus(t, firstList, http.StatusOK)
	var firstPage struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, firstList, &firstPage)
	if len(firstPage.Items) != 1 || firstPage.Items[0]["id"] != recordID {
		t.Fatalf("owner list did not contain exactly the owned record: %#v", firstPage.Items)
	}
	secondList := publicUsersRequest(t, app, http.MethodGet, collectionPath, nil, second.Token, nil, nil)
	assertPublicUsersStatus(t, secondList, http.StatusOK)
	var secondPage struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, secondList, &secondPage)
	if len(secondPage.Items) != 0 {
		t.Fatalf("second user list exposed records: %d", len(secondPage.Items))
	}

	missingPath := collectionPath + "/0199ab10-0000-7000-8000-000000000099"
	for _, operation := range []struct {
		name, method string
		body         any
		headers      map[string]string
	}{
		{name: "get", method: http.MethodGet},
		{name: "patch", method: http.MethodPatch, body: map[string]any{"title": "intrusion"}, headers: map[string]string{"If-Match": createdETag}},
		{name: "delete", method: http.MethodDelete, headers: map[string]string{"If-Match": createdETag}},
	} {
		t.Run("cross-user "+operation.name+" matches missing", func(t *testing.T) {
			foreign := publicUsersRequest(t, app, operation.method, recordPath, operation.body, second.Token, nil, operation.headers)
			missing := publicUsersRequest(t, app, operation.method, missingPath, operation.body, second.Token, nil, operation.headers)
			assertPublicUsersStatus(t, foreign, http.StatusNotFound)
			assertPublicUsersStatus(t, missing, http.StatusNotFound)
			if foreign.Header().Get("ETag") != "" || missing.Header().Get("ETag") != "" {
				t.Fatal("a not-found response disclosed an ETag")
			}
			foreignError := decodePublicUsersError(t, foreign)
			missingError := decodePublicUsersError(t, missing)
			if foreignError != missingError {
				t.Fatalf("foreign and missing record errors differ: foreign=%+v missing=%+v",
					foreignError, missingError)
			}
		})
	}

	owned := publicUsersRequest(t, app, http.MethodGet, recordPath, nil, first.Token, nil, nil)
	assertPublicUsersStatus(t, owned, http.StatusOK)
	if owned.Header().Get("ETag") != createdETag {
		t.Fatalf("owner GET ETag changed: got %q", owned.Header().Get("ETag"))
	}
	updated := publicUsersRequest(t, app, http.MethodPatch, recordPath,
		map[string]any{"title": "updated note"}, first.Token, nil, map[string]string{"If-Match": createdETag})
	assertPublicUsersStatus(t, updated, http.StatusOK)
	updatedETag := updated.Header().Get("ETag")
	if updatedETag == "" || updatedETag == createdETag {
		t.Fatal("owner PATCH did not advance the ETag")
	}
	deleted := publicUsersRequest(t, app, http.MethodDelete, recordPath, nil, first.Token, nil,
		map[string]string{"If-Match": updatedETag})
	assertPublicUsersStatus(t, deleted, http.StatusNoContent)

	login := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": first.User.Email, "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, login, http.StatusOK)
	var loggedIn publicUserSession
	decodePublicUsersJSON(t, login, &loggedIn)
	if loggedIn.Token == "" || loggedIn.User.ID != first.User.ID {
		t.Fatal("login did not return the registered user session")
	}
	session := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/session", nil, loggedIn.Token, nil, nil)
	assertPublicUsersStatus(t, session, http.StatusOK)
	logout := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/logout", nil, loggedIn.Token, nil, nil)
	assertPublicUsersStatus(t, logout, http.StatusNoContent)
	expired := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/session", nil, loggedIn.Token, nil, nil)
	assertPublicUsersStatus(t, expired, http.StatusUnauthorized)
}

func newPublicUsersApp(t *testing.T, registrationEnabled bool) *publicUsersFixture {
	t.Helper()
	publicURL, err := url.Parse(publicUsersOrigin)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{
		DatabaseURL:             testutil.NewDatabase(t),
		HTTPAddress:             unusedAddress(t),
		PublicURL:               publicURL,
		SetupToken:              publicSetupToken,
		TokenSecret:             []byte("public-users-token-secret-32-bytes-minimum"),
		AutoMigrate:             true,
		UserRegistrationEnabled: registrationEnabled,
		Logger:                  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		cancel()
		_ = app.Close(context.Background())
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = app.Close(closeContext)
	})
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	fixture := &publicUsersFixture{app: app, server: server}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ready := publicUsersRequest(t, fixture, http.MethodGet, "/health/ready", nil, "", nil, nil)
		if ready.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("app did not become ready: status=%d body=%s", ready.Code, ready.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fixture
}

func setupPublicUsersAdministrator(t *testing.T, app *publicUsersFixture) (*http.Cookie, string) {
	t.Helper()
	response := publicUsersRequest(t, app, http.MethodPost, "/api/admin/setup", map[string]any{
		"setupToken": publicSetupToken, "email": publicAdminEmail, "password": publicAdminPass,
	}, "", nil, nil)
	assertPublicUsersStatus(t, response, http.StatusCreated)
	var session publicAdminSession
	decodePublicUsersJSON(t, response, &session)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" || session.CSRFToken == "" {
		t.Fatal("administrator setup did not issue cookie and CSRF credentials")
	}
	return cookies[0], session.CSRFToken
}

func createPublicUsersCollection(t *testing.T, app *publicUsersFixture, cookie *http.Cookie, csrf, name string) {
	t.Helper()
	change := map[string]any{
		"operation": "create", "name": name,
		"fields": []map[string]any{{"name": "title", "type": "text", "required": true}},
	}
	plan := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie,
		map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"] = planned.BaseVersion
	change["checksum"] = planned.Checksum
	applied := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie,
		map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, applied, http.StatusCreated)
}

func registerPublicUser(t *testing.T, app *publicUsersFixture, email, password string) publicUserSession {
	t.Helper()
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register",
		map[string]any{"email": email, "password": password}, "", nil, nil)
	assertPublicUsersStatus(t, response, http.StatusCreated)
	var session publicUserSession
	decodePublicUsersJSON(t, response, &session)
	if session.User.Email != email {
		t.Fatalf("registered email = %q, want %q", session.User.Email, email)
	}
	return session
}

func publicUsersRequest(t *testing.T, app *publicUsersFixture, method, path string, body any, bearer string,
	cookie *http.Cookie, headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, app.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "pgbase.test"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", publicUsersOrigin)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	result, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("serve %s %s: %v", method, path, err)
	}
	defer result.Body.Close()
	response := httptest.NewRecorder()
	response.Code = result.StatusCode
	response.HeaderMap = result.Header.Clone()
	if _, err := io.Copy(response.Body, result.Body); err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return response
}

func assertPublicUsersStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		body := strings.TrimSpace(response.Body.String())
		t.Fatalf("HTTP status = %d, want %d, response=%q", response.Code, want, body)
	}
}

func decodePublicUsersJSON(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode HTTP %d response: %v", response.Code, err)
	}
}

func decodePublicUsersError(t *testing.T, response *httptest.ResponseRecorder) struct {
	Code    string
	Message string
} {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodePublicUsersJSON(t, response, &envelope)
	return struct {
		Code    string
		Message string
	}{Code: envelope.Error.Code, Message: envelope.Error.Message}
}
