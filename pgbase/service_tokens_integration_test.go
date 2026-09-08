//go:build integration

package pgbase

import (
	"github.com/google/uuid"
	"net/http"
	"testing"
)

func TestServiceTokenHTTPScopesAndRevocation(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "service_notes")
	var collectionID uuid.UUID
	if err := app.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='service_notes'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	issued := publicUsersRequest(t, app, http.MethodPost, "/api/admin/service-tokens", map[string]any{"name": "test worker", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}, {"collectionId": collectionID.String(), "action": "create"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, http.StatusCreated)
	var token struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &token)
	if token.Token == "" {
		t.Fatal("token not returned")
	}
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/service/collections/service_notes/records", map[string]any{"title": "service record"}, token.Token, nil, map[string]string{"Idempotency-Key": "service-create-record-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	listed := publicUsersRequest(t, app, http.MethodGet, "/api/v1/service/collections/service_notes/records", nil, token.Token, nil, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/service/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "service_notes", "input": map[string]any{"title": "batch service record"}, "idempotencyKey": "service-batch-create-0001"}}}, token.Token, nil, nil)
	assertPublicUsersStatus(t, batch, http.StatusOK)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	id := record["id"].(string)
	denied := publicUsersRequest(t, app, http.MethodDelete, "/api/v1/service/collections/service_notes/records/"+id, nil, token.Token, nil, map[string]string{"If-Match": created.Header().Get("ETag")})
	assertPublicUsersStatus(t, denied, http.StatusForbidden)
	adminList := publicUsersRequest(t, app, http.MethodGet, "/api/admin/service-tokens", nil, "", cookie, nil)
	assertPublicUsersStatus(t, adminList, http.StatusOK)
	if adminList.Body.String() == "" || contains(adminList.Body.String(), token.Token) {
		t.Fatal("admin list exposed raw token")
	}
	revoked := publicUsersRequest(t, app, http.MethodPost, "/api/admin/service-tokens/"+token.ID+"/revoke", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, revoked, http.StatusNoContent)
	after := publicUsersRequest(t, app, http.MethodGet, "/api/v1/service/collections/service_notes/records", nil, token.Token, nil, nil)
	assertPublicUsersStatus(t, after, http.StatusUnauthorized)
	confused := publicUsersRequest(t, app, http.MethodGet, "/api/v1/public/collections/service_notes/records", nil, token.Token, nil, nil)
	assertPublicUsersStatus(t, confused, http.StatusUnauthorized)

	activeIssued := publicUsersRequest(t, app, http.MethodPost, "/api/admin/service-tokens", map[string]any{"name": "active during collection drop", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, activeIssued, http.StatusCreated)
	var activeToken struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, activeIssued, &activeToken)
	drop := map[string]any{"operation": "delete", "name": "service_notes"}
	planAndApplySchemaChange(t, app, cookie, csrf, drop, http.StatusOK, nil)
	var scopes int
	if err := app.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.service_token_scopes where collection_id=$1`, collectionID).Scan(&scopes); err != nil {
		t.Fatal(err)
	}
	if scopes != 0 {
		t.Fatalf("service token scopes after collection deletion = %d", scopes)
	}
	activeAfterDrop := publicUsersRequest(t, app, http.MethodGet, "/api/v1/service/collections/service_notes/records", nil, activeToken.Token, nil, nil)
	assertPublicUsersStatus(t, activeAfterDrop, http.StatusNotFound)
}
func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
