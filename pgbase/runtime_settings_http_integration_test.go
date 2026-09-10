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
	response := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/settings", map[string]any{"revision": initial["revision"], "batch": map[string]any{"enabled": false}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	updated := get()
	if updated["batch"].(map[string]any)["enabled"] != false || updated["revision"].(float64) <= initial["revision"].(float64) {
		t.Fatalf("updated settings = %#v", updated)
	}
	disabled := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []any{}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, disabled, http.StatusServiceUnavailable)
	if got := decodePublicUsersError(t, disabled).Code; got != "batch_disabled" {
		t.Fatalf("disabled batch code = %q", got)
	}
	stale := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/settings", map[string]any{"revision": initial["revision"], "batch": map[string]any{"enabled": true}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, stale, http.StatusConflict)
}
