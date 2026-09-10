//go:build integration

package pgbase

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
)

func TestGeneratedFieldsHTTPNeverExposesPasswordMaterial(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	administrator, csrf := setupPublicUsersAdministrator(t, fixture)
	change := map[string]any{"operation": "create", "name": "http_credentials", "fields": []map[string]any{
		{"name": "label", "type": "text", "required": true},
		{"name": "changed", "type": "autodate", "indexed": true, "options": map[string]any{"onCreate": true, "onUpdate": true}},
		{"name": "password", "type": "password", "required": true, "options": map[string]any{"minLength": 4}},
		{"name": "private_password", "type": "password", "hidden": true},
	}}
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	preview := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/plan", change, "", administrator, headers)
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/apply", change, "", administrator, headers), http.StatusCreated)

	plaintext, hidden := "http-swordfish", "http-hidden-secret"
	createHeaders := map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "http-generated-fields-0001"}
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/http_credentials/records",
		map[string]any{"label": "one", "password": plaintext, "private_password": hidden}, "", administrator, createHeaders)
	assertPublicUsersStatus(t, created, http.StatusCreated)
	assertHTTPPasswordRedacted(t, created.Body.String(), plaintext, hidden)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	if record["password"] != "" || record["private_password"] != "" {
		t.Fatalf("create response password fields=%#v", record)
	}
	id := uuid.MustParse(record["id"].(string))
	for _, check := range []struct {
		value string
		want  bool
	}{{plaintext, true}, {"wrong-password", false}} {
		matched, err := fixture.app.VerifyRecordPassword(t.Context(), "http_credentials", id, "password", check.value)
		if err != nil || matched != check.want {
			t.Fatalf("public verifier match=%v err=%v", matched, err)
		}
	}
	if matched, err := fixture.app.VerifyRecordPassword(t.Context(), "http_credentials", uuid.New(), "password", plaintext); err != nil || matched {
		t.Fatalf("missing-record match=%v err=%v", matched, err)
	}
	if _, err := fixture.app.VerifyRecordPassword(t.Context(), "http_credentials", id, "label", plaintext); err == nil {
		t.Fatal("verifier accepted non-password field")
	}
	var hash string
	if err := fixture.app.pool.QueryRow(t.Context(), `select password from app.http_credentials where id=$1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := passwordfield.Verify(hash, plaintext); err != nil {
		t.Fatalf("stored password did not verify: %v", err)
	}

	got := publicUsersRequest(t, fixture, http.MethodGet, "/api/v1/collections/http_credentials/records/"+id.String(), nil, "", administrator, nil)
	assertPublicUsersStatus(t, got, http.StatusOK)
	assertHTTPPasswordRedacted(t, got.Body.String(), plaintext, hidden, hash)
	patched := publicUsersRequest(t, fixture, http.MethodPatch, "/api/v1/collections/http_credentials/records/"+id.String(), map[string]any{}, "", administrator, headers)
	assertPublicUsersStatus(t, patched, http.StatusOK)
	assertHTTPPasswordRedacted(t, patched.Body.String(), plaintext, hidden, hash)
	replacement := "replacement-secret"
	patched = publicUsersRequest(t, fixture, http.MethodPatch, "/api/v1/collections/http_credentials/records/"+id.String(), map[string]any{"password": replacement}, "", administrator, headers)
	assertPublicUsersStatus(t, patched, http.StatusOK)
	assertHTTPPasswordRedacted(t, patched.Body.String(), plaintext, replacement, hash)
	if err := fixture.app.pool.QueryRow(t.Context(), `select password from app.http_credentials where id=$1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := passwordfield.Verify(hash, replacement); err != nil {
		t.Fatalf("replacement password did not verify: %v", err)
	}
	var artifacts string
	if err := fixture.app.pool.QueryRow(t.Context(), `select coalesce(string_agg(value,''),'') from (
		select response_body::text value from pgbase_meta.record_idempotency where record_id=$1
		union all select envelope::text from pgbase_meta.record_outbox where record_id=$1
		union all select record_snapshot::text from pgbase_meta.record_outbox where record_id=$1) stored`, id).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	assertHTTPPasswordRedacted(t, artifacts, plaintext, replacement, hidden, hash)
}

func assertHTTPPasswordRedacted(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Fatalf("HTTP response exposed password material")
		}
	}
}
