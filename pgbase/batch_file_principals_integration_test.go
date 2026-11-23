//go:build integration

package pgbase

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/google/uuid"
)

// A multipart batch attaches files with the same authority as the
// single-record file path of its principal. Users and administrators attach
// files on both. A service token has no single-record way to attach a file: the
// record routes take JSON only, there is no service file route, and the file
// service refuses service principals; its batch is refused the same way and
// rolls back. Anonymous callers keep the requests-first rule and the same
// refusal.
func TestBatchFileAttachmentFollowsEachPrincipalsSingleRecordAuthority(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, app)
	adminHeaders := map[string]string{"X-PgBase-CSRF": csrf}
	planAndApplySchemaChange(t, app, admin, csrf, map[string]any{"operation": "create", "name": "batch_files", "fields": []map[string]any{
		{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file"},
	}}, http.StatusCreated, nil)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/batch_files/user-access", map[string]any{"enabled": true}, "", admin, adminHeaders), http.StatusOK)
	allow := map[string]any{"op": "allow"}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/batch_files/rules", map[string]any{"rules": map[string]any{"list": allow, "view": allow, "create": allow, "update": allow, "delete": allow}}, "", admin, adminHeaders), http.StatusOK)
	var collectionID uuid.UUID
	if err := app.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='batch_files'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	scopes := []map[string]any{}
	for _, action := range []string{"read", "create", "update", "delete"} {
		scopes = append(scopes, map[string]any{"collectionId": collectionID.String(), "action": action})
	}
	issued := publicUsersRequest(t, app, http.MethodPost, "/api/admin/service-tokens", map[string]any{"name": "batch files worker", "ttlSeconds": 3600, "scopes": scopes}, "", admin, adminHeaders)
	assertPublicUsersStatus(t, issued, http.StatusCreated)
	var token struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &token)
	user := registerPublicUser(t, app, "batch-files@example.com", "correct horse battery staple")

	type outcome struct {
		status int
		code   string
	}
	send := func(method, path, bearer, contentType string, body io.Reader) (outcome, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, app.server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "pgbase.test"
		request.Header.Set("Origin", publicUsersOrigin)
		request.Header.Set("Content-Type", contentType)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return outcome{response.StatusCode, envelope.Error.Code}, raw
	}
	batch := func(title string, filesFirst bool) (io.Reader, string) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		writeFile := func() {
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", `form-data; name="upload"; filename="upload.txt"`)
			header.Set("Content-Type", "text/plain")
			file, _ := writer.CreatePart(header)
			_, _ = file.Write([]byte("batch file bytes"))
		}
		if filesFirst {
			writeFile()
		}
		requests, _ := writer.CreateFormField("requests")
		_, _ = requests.Write([]byte(`{"operations":[{"operation":"create","collection":"batch_files","input":{"title":"` + title + `","attachment":{"$file":"upload"}}}]}`))
		if !filesFirst {
			writeFile()
		}
		_ = writer.Close()
		return &body, writer.FormDataContentType()
	}
	countRows := func(title string) (records, files int) {
		t.Helper()
		if err := app.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.record_files where collection_id=$1 and status='ready'`, collectionID).Scan(&files); err != nil {
			t.Fatal(err)
		}
		listed := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/batch_files/records", nil, "", admin, nil)
		assertPublicUsersStatus(t, listed, http.StatusOK)
		var page struct {
			Items []map[string]any `json:"items"`
		}
		decodePublicUsersJSON(t, listed, &page)
		for _, item := range page.Items {
			if item["title"] == title {
				records++
			}
		}
		return records, files
	}

	// The service token's single-record path cannot attach a file: there is
	// no service file route, and the user file route does not accept it.
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/service/collections/batch_files/records", map[string]any{"title": "service single"}, token.Token, nil, map[string]string{"Idempotency-Key": "batch-files-service-single-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	recordID := record["id"].(string)
	for _, path := range []string{
		"/api/v1/service/collections/batch_files/records/" + recordID + "/files/attachment",
		"/api/v1/user/collections/batch_files/records/" + recordID + "/files/attachment",
	} {
		body, contentType := batch("unused", false)
		if got, _ := send(http.MethodPost, path, token.Token, contentType, body); got.status < 400 {
			t.Fatalf("service token single-record file upload %s = %+v, want a refusal", path, got)
		}
	}

	// Its batch is refused as the file service refuses it, and nothing stays.
	_, filesBefore := countRows("service batch")
	body, contentType := batch("service batch", false)
	if got, raw := send(http.MethodPost, "/api/v1/service/collections/batch", token.Token, contentType, body); got != (outcome{http.StatusForbidden, "file_access_denied"}) {
		t.Fatalf("service token multipart batch = %+v %s, want 403 file_access_denied", got, raw)
	}
	if records, files := countRows("service batch"); records != 0 || files != filesBefore {
		t.Fatalf("refused service batch left %d records and %d -> %d attached files", records, filesBefore, files)
	}

	// A user attaches through the batch exactly as through its file route.
	body, contentType = batch("user batch", false)
	if got, raw := send(http.MethodPost, "/api/v1/user/collections/batch", user.Token, contentType, body); got.status != http.StatusOK {
		t.Fatalf("user multipart batch = %+v %s, want 200", got, raw)
	}
	if records, files := countRows("user batch"); records != 1 || files != filesBefore+1 {
		t.Fatalf("user batch stored %d records and %d -> %d attached files", records, filesBefore, files)
	}

	// Anonymous callers keep the requests-first rule and the file refusal.
	body, contentType = batch("anonymous batch", true)
	if got, raw := send(http.MethodPost, "/api/v1/public/collections/batch", "", contentType, body); got != (outcome{http.StatusBadRequest, "invalid_request"}) {
		t.Fatalf("anonymous batch with files first = %+v %s, want 400 invalid_request", got, raw)
	}
	body, contentType = batch("anonymous batch", false)
	if got, raw := send(http.MethodPost, "/api/v1/public/collections/batch", "", contentType, body); got != (outcome{http.StatusForbidden, "file_access_denied"}) {
		t.Fatalf("anonymous multipart batch = %+v %s, want 403 file_access_denied", got, raw)
	}
	if records, files := countRows("anonymous batch"); records != 0 || files != filesBefore+1 {
		t.Fatalf("refused anonymous batch left %d records and %d attached files", records, files)
	}
}
