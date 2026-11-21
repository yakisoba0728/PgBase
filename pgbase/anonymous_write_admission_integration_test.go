//go:build integration

package pgbase

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"testing"
)

// Anonymous writes to a collection without the public opt-in, or to a missing
// collection, are refused before the body is read or a batch file is staged.
// The refusal must be the response the record service produced when it read
// the body first; these expectations were recorded from the handlers with the
// preflight disabled.
func TestAnonymousWriteAdmissionPreflightKeepsRecordServiceResponses(t *testing.T) {
	storage := t.TempDir()
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: storage, MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, app)
	for _, name := range []string{"anon_open", "anon_closed"} {
		planAndApplySchemaChange(t, app, admin, csrf, map[string]any{"operation": "create", "name": name, "fields": []map[string]any{
			{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file"},
		}}, http.StatusCreated, nil)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/anon_open/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/anon_closed/records", map[string]any{"title": "kept"}, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	closedID := record["id"].(string)

	type outcome struct {
		status int
		code   string
	}
	result := func(response *http.Response) outcome {
		t.Helper()
		defer response.Body.Close()
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		raw, _ := io.ReadAll(response.Body)
		_ = json.Unmarshal(raw, &envelope)
		return outcome{response.StatusCode, envelope.Error.Code}
	}
	send := func(method, path, contentType string, body io.Reader) outcome {
		t.Helper()
		request, err := http.NewRequest(method, app.server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "pgbase.test"
		request.Header.Set("Origin", publicUsersOrigin)
		request.Header.Set("Content-Type", contentType)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return result(response)
	}
	jsonBody := func(value any) io.Reader {
		encoded, _ := json.Marshal(value)
		return bytes.NewReader(encoded)
	}
	// An anonymous multipart batch must carry its requests part ahead of every
	// file part; filesFirst builds the order that is refused.
	multipartBatchOrdered := func(collection string, filesFirst bool) (io.Reader, string) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		writeFile := func() {
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", `form-data; name="upload"; filename="upload.txt"`)
			header.Set("Content-Type", "text/plain")
			file, _ := writer.CreatePart(header)
			_, _ = file.Write(bytes.Repeat([]byte{'z'}, 64<<10))
		}
		if filesFirst {
			writeFile()
		}
		requests, _ := writer.CreateFormField("requests")
		_, _ = requests.Write([]byte(`{"operations":[{"operation":"create","collection":"anon_open","input":{"title":"a"}},{"operation":"create","collection":"` + collection + `","input":{"title":"b","attachment":{"$file":"upload"}}}]}`))
		if !filesFirst {
			writeFile()
		}
		_ = writer.Close()
		return &body, writer.FormDataContentType()
	}
	multipartBatch := func(collection string) (io.Reader, string) { return multipartBatchOrdered(collection, false) }

	for _, test := range []struct {
		name string
		do   func() outcome
		want outcome
	}{
		{"create closed", func() outcome {
			return send(http.MethodPost, "/api/v1/public/collections/anon_closed/records", "application/json", jsonBody(map[string]any{"title": "x"}))
		}, outcome{http.StatusForbidden, "access_denied"}},
		{"create missing", func() outcome {
			return send(http.MethodPost, "/api/v1/public/collections/anon_missing/records", "application/json", jsonBody(map[string]any{"title": "x"}))
		}, outcome{http.StatusNotFound, "not_found"}},
		{"update closed existing", func() outcome {
			return send(http.MethodPatch, "/api/v1/public/collections/anon_closed/records/"+closedID, "application/json", jsonBody(map[string]any{"title": "x"}))
		}, outcome{http.StatusForbidden, "access_denied"}},
		{"update missing", func() outcome {
			return send(http.MethodPatch, "/api/v1/public/collections/anon_missing/records/"+closedID, "application/json", jsonBody(map[string]any{"title": "x"}))
		}, outcome{http.StatusNotFound, "not_found"}},
		{"multipart batch closed", func() outcome {
			body, contentType := multipartBatch("anon_closed")
			return send(http.MethodPost, "/api/v1/public/collections/batch", contentType, body)
		}, outcome{http.StatusForbidden, "access_denied"}},
		{"multipart batch missing", func() outcome {
			body, contentType := multipartBatch("anon_missing")
			return send(http.MethodPost, "/api/v1/public/collections/batch", contentType, body)
		}, outcome{http.StatusBadRequest, "invalid_request"}},
		{"multipart batch files before requests", func() outcome {
			body, contentType := multipartBatchOrdered("anon_open", true)
			return send(http.MethodPost, "/api/v1/public/collections/batch", contentType, body)
		}, outcome{http.StatusBadRequest, "invalid_request"}},
	} {
		if got := test.do(); got != test.want {
			t.Errorf("%s = %+v, want %+v", test.name, got, test.want)
		}
	}

	// The opt-in collection with permissive rules keeps working.
	allow := map[string]any{"op": "allow"}
	rules := map[string]any{"rules": map[string]any{"list": allow, "view": allow, "create": allow, "update": allow, "delete": allow}}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/anon_open/rules", rules, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	if open := send(http.MethodPost, "/api/v1/public/collections/anon_open/records", "application/json", jsonBody(map[string]any{"title": "x"})); open.status != http.StatusCreated {
		t.Fatalf("opt-in anonymous create = %+v, want 201", open)
	}
	// It passes the preflight and the record service, but anonymous callers
	// may not attach files: the file service refuses inside the transaction,
	// which is answered as the file endpoints answer it, not as a 500, and
	// rolls back the records and the staged bytes.
	countRecords := func() int {
		t.Helper()
		listed := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/anon_open/records", nil, "", admin, nil)
		assertPublicUsersStatus(t, listed, http.StatusOK)
		var page struct {
			Items []map[string]any `json:"items"`
		}
		decodePublicUsersJSON(t, listed, &page)
		return len(page.Items)
	}
	countStoredFiles := func() int {
		t.Helper()
		count := 0
		if err := filepath.WalkDir(storage, func(_ string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				count++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	countFileRows := func() (attached, queued int) {
		t.Helper()
		if err := app.app.pool.QueryRow(t.Context(), `select (select count(*) from pgbase_meta.record_files), (select count(*) from pgbase_meta.file_object_deletions)`).Scan(&attached, &queued); err != nil {
			t.Fatal(err)
		}
		return attached, queued
	}
	recordsBefore, filesBefore := countRecords(), countStoredFiles()
	attachedBefore, queuedBefore := countFileRows()
	body, contentType := multipartBatch("anon_open")
	if batch := send(http.MethodPost, "/api/v1/public/collections/batch", contentType, body); batch != (outcome{http.StatusForbidden, "file_access_denied"}) {
		t.Fatalf("opt-in anonymous multipart batch = %+v, want 403 file_access_denied", batch)
	}
	// The staged bytes are not attached; every object they left is queued for
	// deletion, as for any rolled-back batch.
	records, files := countRecords(), countStoredFiles()
	attached, queued := countFileRows()
	if records != recordsBefore || attached != attachedBefore || files-filesBefore != queued-queuedBefore {
		t.Fatalf("refused batch: records %d -> %d, attached files %d -> %d, stored objects %d -> %d with %d queued for deletion", recordsBefore, records, attachedBefore, attached, filesBefore, files, queued-queuedBefore)
	}
}
