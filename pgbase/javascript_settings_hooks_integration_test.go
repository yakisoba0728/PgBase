//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptPocketBaseSettingsHooksReachHTTPPhases(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","hooks":[{"event":"settings.list","before":{"module":"route.js","export":"run"}},{"event":"settings.update","before":{"module":"route.js","export":"run"}}]}`
	writeJavaScriptFixture(t, directory, manifest, `exports.run=()=>({})`)
	bootstrap := `onSettingsListRequest((e)=>e.next());onSettingsUpdateRequest(()=>{throw new Error("settings update veto")})`
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(o *Options) {
		o.JavaScript = &JavaScriptOptions{Directory: directory}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	listed := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/settings", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	var settings map[string]any
	decodePublicUsersJSON(t, listed, &settings)
	updated := publicUsersRequest(t, fixture, http.MethodPatch, "/api/admin/settings", map[string]any{"revision": settings["revision"]}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	if updated.Code < 400 {
		t.Fatalf("vetoed settings update status=%d body=%s", updated.Code, updated.Body.String())
	}
}
