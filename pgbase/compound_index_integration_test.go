//go:build integration

package pgbase

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestGuardedHTTPApplyAddsCompoundIndex(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	fields := []map[string]any{{"name": "category", "type": "text"}, {"name": "title", "type": "text"}}
	create := map[string]any{"operation": "create", "name": "guarded_compound_http", "fields": fields}
	planAndApplySchemaChange(t, app, cookie, csrf, create, http.StatusCreated, nil)

	update := map[string]any{"operation": "update", "name": "guarded_compound_http", "fields": fields, "indexes": []map[string]any{{"name": "by_category_title", "fields": []string{"category", "title"}, "unique": false}}}
	headers := map[string]string{"PgBase-Operation-ID": uuid.New().String(), "Idempotency-Key": "guarded-compound-http-0001"}
	planAndApplySchemaChange(t, app, cookie, csrf, update, http.StatusOK, headers)
}

func planAndApplySchemaChange(t *testing.T, app *publicUsersFixture, cookie *http.Cookie, csrf string, change map[string]any, want int, guarded map[string]string) {
	t.Helper()
	plan := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var approved struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &approved)
	change["baseVersion"], change["checksum"] = approved.BaseVersion, approved.Checksum
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	for key, value := range guarded {
		headers[key] = value
	}
	applied := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, headers)
	assertPublicUsersStatus(t, applied, want)
}
