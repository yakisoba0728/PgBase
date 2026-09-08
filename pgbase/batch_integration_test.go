//go:build integration

package pgbase

import (
	"net/http"
	"testing"
)

func TestPublicBatchDenialRollsBackEarlierOperationHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "notes")
	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	first := registerPublicUser(t, app, "batch-first@example.test", publicUserOnePass)
	second := registerPublicUser(t, app, "batch-second@example.test", publicUserTwoPass)
	path := "/api/v1/user/collections/notes/records"
	seed := publicUsersRequest(t, app, http.MethodPost, path, map[string]any{"title": "first private"}, first.Token, nil, map[string]string{"Idempotency-Key": "batch-seed-owner-0001"})
	assertPublicUsersStatus(t, seed, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, seed, &record)
	foreignID := record["id"].(string)

	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "notes", "input": map[string]any{"title": "must roll back"}, "idempotencyKey": "batch-owner-create-0001"},
		{"operation": "update", "collection": "notes", "id": foreignID, "input": map[string]any{"title": "intrusion"}},
	}}, second.Token, nil, nil)
	assertPublicUsersStatus(t, batch, http.StatusNotFound)
	page := publicUsersRequest(t, app, http.MethodGet, path, nil, second.Token, nil, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	if len(listing.Items) != 0 {
		t.Fatalf("denied batch left owned records: %#v", listing.Items)
	}
}

func TestBatchHTTPDeleteHasExplicitResult(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "notes")
	_ = publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	user := registerPublicUser(t, app, "batch-delete@example.test", publicUserOnePass)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/notes/records", map[string]any{"title": "delete"}, user.Token, nil, map[string]string{"Idempotency-Key": "batch-delete-seed-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "delete", "collection": "notes", "id": record["id"]}}}, user.Token, nil, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var body struct {
		Results []struct {
			EventID string `json:"eventId"`
			Record  any    `json:"record"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, response, &body)
	if len(body.Results) != 1 || body.Results[0].EventID == "" || body.Results[0].Record != nil {
		t.Fatalf("delete batch response=%#v", body)
	}
}
