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
)

// Anonymous writes to a collection without the public opt-in, or to a missing
// collection, are refused before the body is read or a batch file is staged.
// The refusal must be the response the record service produced when it read
// the body first; these expectations were recorded from the handlers with the
// preflight disabled.
func TestAnonymousWriteAdmissionPreflightKeepsRecordServiceResponses(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
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
	multipartBatch := func(collection string) (io.Reader, string) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="upload"; filename="upload.txt"`)
		header.Set("Content-Type", "text/plain")
		file, _ := writer.CreatePart(header)
		_, _ = file.Write(bytes.Repeat([]byte{'z'}, 64<<10))
		requests, _ := writer.CreateFormField("requests")
		_, _ = requests.Write([]byte(`{"operations":[{"operation":"create","collection":"anon_open","input":{"title":"a"}},{"operation":"create","collection":"` + collection + `","input":{"title":"b","attachment":{"$file":"upload"}}}]}`))
		_ = writer.Close()
		return &body, writer.FormDataContentType()
	}

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
	} {
		if got := test.do(); got != test.want {
			t.Errorf("%s = %+v, want %+v", test.name, got, test.want)
		}
	}

	// The opt-in collection with permissive rules keeps working. Its multipart
	// batch passes the preflight and reaches the record and file services; what
	// they answer for an anonymous attachment is outside this preflight, so the
	// test only requires that it is not one of the preflight refusals.
	allow := map[string]any{"op": "allow"}
	rules := map[string]any{"rules": map[string]any{"list": allow, "view": allow, "create": allow, "update": allow, "delete": allow}}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/anon_open/rules", rules, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	if open := send(http.MethodPost, "/api/v1/public/collections/anon_open/records", "application/json", jsonBody(map[string]any{"title": "x"})); open.status != http.StatusCreated {
		t.Fatalf("opt-in anonymous create = %+v, want 201", open)
	}
	body, contentType := multipartBatch("anon_open")
	if batch := send(http.MethodPost, "/api/v1/public/collections/batch", contentType, body); batch.code == "access_denied" || batch.code == "not_found" || batch.code == "invalid_request" {
		t.Fatalf("opt-in anonymous multipart batch = %+v, want it past the admission preflight", batch)
	}
}
