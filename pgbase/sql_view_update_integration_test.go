//go:build integration

package pgbase

import (
	"fmt"
	"testing"
)

func TestSQLViewHTTPDefinitionUpdateChangesReadContent(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	apply := func(change map[string]any, status int) {
		t.Helper()
		preview := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", cookie, headers)
		assertPublicUsersStatus(t, preview, 200)
		var plan struct {
			BaseVersion int64  `json:"baseVersion"`
			Checksum    string `json:"checksum"`
		}
		decodePublicUsersJSON(t, preview, &plan)
		change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", cookie, headers), status)
	}
	apply(map[string]any{"operation": "create", "name": "update_view_source", "fields": []map[string]any{{"name": "amount", "type": "integer"}}}, 201)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/collections/update_view_source/records", map[string]any{"amount": 4}, "", cookie, headers), 201)
	apply(map[string]any{"operation": "create", "name": "update_view_total", "view": map[string]any{"sql": "SELECT id, amount::bigint AS total FROM app.update_view_source"}}, 201)
	read := func() map[string]any {
		response := publicUsersRequest(t, app, "GET", "/api/v1/collections/update_view_total/records", nil, "", cookie, nil)
		assertPublicUsersStatus(t, response, 200)
		var page struct {
			Items []map[string]any `json:"items"`
		}
		decodePublicUsersJSON(t, response, &page)
		if len(page.Items) != 1 {
			t.Fatalf("page=%s", response.Body.String())
		}
		return page.Items[0]
	}
	if fmt.Sprint(read()["total"]) != "4" {
		t.Fatal("initial SQL view content mismatch")
	}
	apply(map[string]any{"operation": "update", "name": "update_view_total", "view": map[string]any{"sql": "SELECT id, (amount * 2)::bigint AS total FROM app.update_view_source"}}, 200)
	if fmt.Sprint(read()["total"]) != "8" {
		t.Fatal("updated SQL view content mismatch")
	}
}
