//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAdministratorS3SettingsHTTPRedactionTestAndRotation(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	app := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}, S3AllowInsecureLoopback: true}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	get := publicUsersRequest(t, app, http.MethodGet, "/api/admin/storage/files", nil, "", cookie, nil)
	assertPublicUsersStatus(t, get, 200)
	var initial struct {
		Revision int64 `json:"revision"`
	}
	decodePublicUsersJSON(t, get, &initial)
	body := map[string]any{"driver": "s3", "bucket": "pgbase-test", "region": "us-east-1", "endpoint": endpoint, "accessKey": "pgbase-test-access", "secretKey": "pgbase-test-secret-0123456789", "usePathStyle": true, "revision": initial.Revision}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/storage/files", body, "", cookie, nil), 403)
	saved := publicUsersRequest(t, app, http.MethodPut, "/api/admin/storage/files", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, saved, 200)
	if strings.Contains(saved.Body.String(), "pgbase-test-secret") || !strings.Contains(saved.Body.String(), `"secretConfigured":true`) {
		t.Fatalf("unredacted settings: %s", saved.Body.String())
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/storage/files/test", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 204)
	body["revision"] = float64(initial.Revision + 1)
	delete(body, "secretKey")
	rotated := publicUsersRequest(t, app, http.MethodPut, "/api/admin/storage/files", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, rotated, 200)
	if strings.Contains(rotated.Body.String(), "secret") && !strings.Contains(rotated.Body.String(), "secretConfigured") {
		t.Fatalf("secret leaked: %s", rotated.Body.String())
	}
	migrationBody := map[string]any{"driver": "local", "bucket": "", "region": "", "endpoint": "", "accessKey": "", "usePathStyle": false}
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/storage/files/migrations/preview", migrationBody, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, 200)
	started := publicUsersRequest(t, app, http.MethodPost, "/api/admin/storage/files/migrations", migrationBody, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": "11111111-1111-4111-8111-111111111111"})
	assertPublicUsersStatus(t, started, http.StatusAccepted)
	var migration struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &migration); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for migration.Status != "committed" {
		if time.Now().After(deadline) {
			t.Fatalf("migration did not commit: %#v", migration)
		}
		time.Sleep(20 * time.Millisecond)
		status := publicUsersRequest(t, app, http.MethodGet, "/api/admin/storage/files/migrations/"+migration.ID, nil, "", cookie, nil)
		assertPublicUsersStatus(t, status, 200)
		if err := json.Unmarshal(status.Body.Bytes(), &migration); err != nil {
			t.Fatal(err)
		}
	}
	active := publicUsersRequest(t, app, http.MethodGet, "/api/admin/storage/files", nil, "", cookie, nil)
	assertPublicUsersStatus(t, active, 200)
	if !strings.Contains(active.Body.String(), `"driver":"local"`) {
		t.Fatalf("migration not activated: %s", active.Body.String())
	}
	replayHeaders := map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": "11111111-1111-4111-8111-111111111111"}
	replayed := publicUsersRequest(t, app, http.MethodPost, "/api/admin/storage/files/migrations", migrationBody, "", cookie, replayHeaders)
	assertPublicUsersStatus(t, replayed, http.StatusAccepted)
	migrationBody["bucket"] = "changed-request"
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/storage/files/migrations", migrationBody, "", cookie, replayHeaders), http.StatusConflict)
}
