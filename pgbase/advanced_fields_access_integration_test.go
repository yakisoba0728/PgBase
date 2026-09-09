//go:build integration

package pgbase

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAdvancedFieldsAcrossUserServiceAnonymousAndAtomicBatch(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "advanced_access", "fields": []map[string]any{
		{"name": "title", "type": "text", "required": true, "presentable": true},
		{"name": "body", "type": "editor", "required": true},
		{"name": "location", "type": "geoPoint", "indexed": true},
		{"name": "changed", "type": "autodate", "options": map[string]any{"onCreate": true, "onUpdate": true}},
		{"name": "credential", "type": "password", "required": true, "options": map[string]any{"minLength": 8}},
	}}
	planAndApplySchemaChange(t, app, admin, csrf, change, http.StatusCreated, nil)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/advanced_access/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)

	user := registerPublicUser(t, app, "advanced-access@example.test", publicUserOnePass)
	input := map[string]any{"title": "user row", "body": "<p>user</p>", "location": map[string]any{"lon": 127.1, "lat": 37.4}, "credential": "user-secret"}
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/advanced_access/records", input, user.Token, nil, map[string]string{"Idempotency-Key": "advanced-user-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var userRecord map[string]any
	decodePublicUsersJSON(t, created, &userRecord)
	assertAdvancedAccessRecord(t, userRecord, "user row", "<p>user</p>")

	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/collections/advanced_access/records", nil, "", nil, nil), http.StatusUnauthorized)
	anonymous := publicUsersRequest(t, app, http.MethodGet, "/api/v1/public/collections/advanced_access/records", nil, "", nil, nil)
	assertPublicUsersStatus(t, anonymous, http.StatusOK)
	if strings.Contains(anonymous.Body.String(), "user row") || strings.Contains(anonymous.Body.String(), "user-secret") {
		t.Fatalf("anonymous rule-filtered list exposed advanced record: %s", anonymous.Body.String())
	}

	var collectionID uuid.UUID
	if err := app.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='advanced_access'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	issued := publicUsersRequest(t, app, http.MethodPost, "/api/admin/service-tokens", map[string]any{"name": "advanced worker", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}, {"collectionId": collectionID.String(), "action": "create"}}}, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, http.StatusCreated)
	var service struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &service)
	serviceCreated := publicUsersRequest(t, app, http.MethodPost, "/api/v1/service/collections/advanced_access/records", map[string]any{"title": "service row", "body": "<p>service</p>", "location": map[string]any{"lon": -73.9, "lat": 40.7}, "credential": "service-secret"}, service.Token, nil, map[string]string{"Idempotency-Key": "advanced-service-create-0001"})
	assertPublicUsersStatus(t, serviceCreated, http.StatusCreated)
	var serviceRecord map[string]any
	decodePublicUsersJSON(t, serviceCreated, &serviceRecord)
	assertAdvancedAccessRecord(t, serviceRecord, "service row", "<p>service</p>")

	batch := map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "advanced_access", "input": map[string]any{"title": "must roll back", "body": "<p>first</p>", "location": map[string]any{"lon": 1, "lat": 1}, "credential": "batch-secret"}, "idempotencyKey": "advanced-batch-first-0001"},
		{"operation": "create", "collection": "advanced_access", "input": map[string]any{"title": "invalid geo", "body": "<p>second</p>", "location": map[string]any{"lon": 181, "lat": 1}, "credential": "batch-secret"}, "idempotencyKey": "advanced-batch-second-0001"},
	}}
	failed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", batch, user.Token, nil, nil)
	if failed.Code < 400 {
		t.Fatalf("invalid advanced batch status=%d body=%s", failed.Code, failed.Body.String())
	}
	listed := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/collections/advanced_access/records", nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	if strings.Contains(listed.Body.String(), "must roll back") || strings.Contains(listed.Body.String(), "batch-secret") {
		t.Fatalf("failed batch leaked or committed advanced values: %s", listed.Body.String())
	}
}

func assertAdvancedAccessRecord(t *testing.T, record map[string]any, title, body string) {
	t.Helper()
	if record["title"] != title || record["body"] != body || record["credential"] != "" {
		t.Fatalf("advanced record=%#v", record)
	}
	if changed, ok := record["changed"].(string); !ok || changed == "" {
		t.Fatalf("autodate missing: %#v", record)
	}
	point, ok := record["location"].(map[string]any)
	if !ok || point["lon"] == nil || point["lat"] == nil {
		t.Fatalf("geo point missing: %#v", record)
	}
}
