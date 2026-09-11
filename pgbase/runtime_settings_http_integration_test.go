//go:build integration

package pgbase

import (
	"fmt"
	"net/http"
	"testing"
)

func TestRuntimeBatchLimitsApplyToKernelAndMultipart(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 4096, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, admin, csrf, "runtime_batch_limits")
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	patch := func(batch map[string]any) {
		get := publicUsersRequest(t, fixture, "GET", "/api/admin/settings", nil, "", admin, nil)
		assertPublicUsersStatus(t, get, 200)
		var settings map[string]any
		decodePublicUsersJSON(t, get, &settings)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PATCH", "/api/admin/settings", map[string]any{"revision": settings["revision"], "batch": batch}, "", admin, headers), 200)
	}
	patch(map[string]any{"maxRequests": 100})
	requests := make([]map[string]any, 51)
	for i := range requests {
		requests[i] = map[string]any{"method": "POST", "url": "/api/v1/collections/runtime_batch_limits/records", "body": map[string]any{"title": fmt.Sprintf("record-%d", i)}}
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/collections/batch", map[string]any{"requests": requests}, "", admin, headers), 200)
	patch(map[string]any{"maxRequests": 2})
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/collections/batch", map[string]any{"requests": requests[:3]}, "", admin, headers), 400)
	patch(map[string]any{"maxBodyBytes": 1024})
	oversized := multipartBatchRequest(t, fixture, admin, csrf, map[string]any{"requests": requests[:1]}, map[string][]byte{"oversized": make([]byte, 2048)})
	assertPublicUsersStatus(t, oversized, 400)
	var count int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.runtime_batch_limits`).Scan(&count); err != nil || count != 51 {
		t.Fatalf("records=%d err=%v", count, err)
	}
}

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
