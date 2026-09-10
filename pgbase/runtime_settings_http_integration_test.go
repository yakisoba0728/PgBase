//go:build integration

package pgbase

import (
	"net/http"
	"testing"
)

func TestRuntimeSettingsHTTPPersistsValidatedWholeDocument(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	get := func() map[string]any {
		response := publicUsersRequest(t, app, http.MethodGet, "/api/admin/settings", nil, "", cookie, nil)
		assertPublicUsersStatus(t, response, http.StatusOK)
		var value map[string]any
		decodePublicUsersJSON(t, response, &value)
		return value
	}
	initial := get()
	batch := initial["batch"].(map[string]any)
	batch["enabled"] = false
	response := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/settings", initial, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	updated := get()
	if updated["batch"].(map[string]any)["enabled"] != false || updated["revision"].(float64) <= initial["revision"].(float64) {
		t.Fatalf("updated settings = %#v", updated)
	}
	initial["revision"] = float64(1)
	stale := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/settings", initial, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, stale, http.StatusConflict)
}
