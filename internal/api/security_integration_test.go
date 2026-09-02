//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestSecuritySetupCannotBeReused(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	const secondPassword = "security-second-password-7f31"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	var logs bytes.Buffer
	handler := mustRouter(t, Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})

	first := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/setup", `{"setupToken":"`+setupToken+`","email":"admin@example.test","password":"`+securityPassword+`"}`)
	first.Header.Set("Origin", "http://pgbase.test")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first setup status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	second := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/setup", `{"setupToken":"`+setupToken+`","email":"second@example.test","password":"`+secondPassword+`"}`)
	second.Header.Set("Origin", "http://pgbase.test")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	assertSecurityError(t, secondResponse, http.StatusConflict, "setup_unavailable", setupToken, secondPassword)
	assertSecuritySecretsAbsent(t, logs.String(), setupToken, secondPassword)
}

func TestSecuritySetupIsUnavailableWithoutConfiguredTokenAndCreatesNoState(t *testing.T) {
	pool := newSecurityDatabase(t)
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, "", time.Hour, nil)
	handler := mustRouter(t, Dependencies{
		AdminAuth:   auth,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/setup", `{"setupToken":"","email":"admin@example.test","password":"`+securityPassword+`"}`)
	request.Header.Set("Origin", "http://pgbase.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertSecurityError(t, response, http.StatusConflict, "setup_unavailable", securityPassword)

	var administrators, sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&administrators); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if administrators != 0 || sessions != 0 {
		t.Fatalf("disabled setup persisted administrators=%d sessions=%d, want 0/0", administrators, sessions)
	}
}

func TestSecurityMalformedPasswordHashReturnsGenericCredentialsError(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	const malformedHash = "security-malformed-hash-7f31-postgres://admin:secret@db/private"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	if _, err := auth.Setup(context.Background(), setupToken, "admin@example.test", securityPassword, "setup-client"); err != nil {
		t.Fatalf("seed administrator: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.admins set password_hash = $1 where email = $2`, malformedHash, "admin@example.test"); err != nil {
		t.Fatalf("replace password hash: %v", err)
	}
	var logs bytes.Buffer
	handler := mustRouter(t, Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/auth/login", `{"email":"admin@example.test","password":"`+securityPassword+`"}`)
	request.Header.Set("Origin", "http://pgbase.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusUnauthorized, "invalid_credentials", malformedHash, securityPassword, "postgres://", "7f31")
	assertSecuritySecretsAbsent(t, logs.String(), malformedHash, securityPassword, "postgres://", "7f31")
}

func TestSecurityTamperedCursorIsRejectedWithoutDisclosure(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	session, err := auth.Setup(context.Background(), setupToken, "admin@example.test", securityPassword, "setup-client")
	if err != nil {
		t.Fatalf("seed administrator: %v", err)
	}
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	input := schema.CreateCollectionInput{Name: "records"}
	plan, err := schemas.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatalf("plan records collection: %v", err)
	}
	collection, err := schemas.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, session.Administrator.ID)
	if err != nil {
		t.Fatalf("create records collection: %v", err)
	}
	records := recordops.NewService(pool, schemas, testTokenSecret)
	codec := cursor.NewCodec(testTokenSecret)
	valid, err := codec.Encode(cursor.Page{
		Version:      cursor.Version4,
		CollectionID: collection.ID,
		Sort:         []cursor.SortKey{{Field: "created_at"}, {Field: "id"}},
		Values: []json.RawMessage{
			json.RawMessage(`"2026-08-28T00:00:00Z"`),
			json.RawMessage(`"018f1f09-7b44-7cc1-98e8-6f9a46a17d21"`),
		},
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	tampered := valid[:5] + differentSecurityCharacter(valid[5]) + valid[6:]
	var logs bytes.Buffer
	handler := mustRouter(t, Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		Records:     records,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	request := httptest.NewRequest(http.MethodGet, "http://pgbase.test/api/v1/collections/records/records?cursor="+url.QueryEscape(tampered), nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusBadRequest, "invalid_cursor", tampered)
	assertSecuritySecretsAbsent(t, logs.String(), tampered)
}
