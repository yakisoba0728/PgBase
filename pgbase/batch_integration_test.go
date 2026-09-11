//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
)

func TestMultipartBatchAtomicallyCreatesRecordAndFile(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "batch_documents", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file", "required": true, "options": map[string]any{"maxSelect": 4}}}}
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		Checksum    string `json:"checksum"`
		BaseVersion int64  `json:"baseVersion"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["checksum"], change["baseVersion"] = plan.Checksum, plan.BaseVersion
	applied := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": "019551d0-e9c0-7000-8000-000000000191", "Idempotency-Key": "multipart-schema-0001"})
	assertPublicUsersStatus(t, applied, http.StatusCreated)
	directMissing := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch_documents/records", map[string]any{"title": "missing direct"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "required-file-direct-0001"})
	assertPublicUsersStatus(t, directMissing, http.StatusUnprocessableEntity)
	if !bytes.Contains(directMissing.Body.Bytes(), []byte(`"attachment":["required_file"]`)) {
		t.Fatalf("direct required-file response=%s", directMissing.Body.String())
	}
	missingBatch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/batch_documents/records", "headers": map[string]string{"Idempotency-Key": "required-file-batch-0001"}, "body": map[string]any{"title": "missing batch"}}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, missingBatch, http.StatusUnprocessableEntity)
	if !bytes.Contains(missingBatch.Body.Bytes(), []byte(`"attachment":["required_file"]`)) {
		t.Fatalf("batch required-file response=%s", missingBatch.Body.String())
	}
	requests := map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/batch_documents/records", "headers": map[string]string{"Idempotency-Key": "multipart-record-0001"}, "body": map[string]any{"title": "with file", "attachment": map[string]any{"$file": "attachment_0"}}}}}
	response := multipartBatchRequest(t, app, cookie, csrf, requests, map[string][]byte{"attachment_0": []byte("atomic file")})
	assertPublicUsersStatus(t, response, http.StatusOK)
	var output struct {
		Results []struct {
			Body map[string]any `json:"body"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, response, &output)
	id := output.Results[0].Body["id"].(string)
	listed := publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections/batch_documents/records/"+id+"/files/attachment", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	var files struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, listed, &files)
	if len(files.Items) != 1 || files.Items[0]["filename"] != "attachment_0.txt" {
		t.Fatalf("files=%#v", files.Items)
	}
	replacements := map[string]any{"requests": []map[string]any{
		{"method": "PUT", "url": "/api/v1/collections/batch_documents/records/" + id, "body": map[string]any{"title": "first replacement", "attachment": map[string]any{"$file": "replacement_a"}}},
		{"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/" + id, "body": map[string]any{"attachment": map[string]any{"$file": "replacement_b"}}},
	}}
	replaced := multipartBatchRequest(t, app, cookie, csrf, replacements, map[string][]byte{"replacement_a": []byte("first"), "replacement_b": []byte("second")})
	assertPublicUsersStatus(t, replaced, http.StatusOK)
	listed = publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections/batch_documents/records/"+id+"/files/attachment", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	decodePublicUsersJSON(t, listed, &files)
	if len(files.Items) != 1 || files.Items[0]["filename"] != "replacement_b.txt" {
		t.Fatalf("ordered replacement files=%#v", files.Items)
	}
	appendRequest := map[string]any{"requests": []map[string]any{{
		"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/" + id,
		"body": map[string]any{"attachment+": map[string]any{"$file": "appended"}},
	}}}
	appended := multipartBatchRequest(t, app, cookie, csrf, appendRequest, map[string][]byte{"appended": []byte("append")})
	assertPublicUsersStatus(t, appended, http.StatusOK)
	prependRequest := map[string]any{"requests": []map[string]any{{
		"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/" + id,
		"body": map[string]any{"+attachment": map[string]any{"$file": "prepended"}},
	}}}
	prepended := multipartBatchRequest(t, app, cookie, csrf, prependRequest, map[string][]byte{"prepended": []byte("prepend")})
	assertPublicUsersStatus(t, prepended, http.StatusOK)
	removeRequest := map[string]any{"requests": []map[string]any{{
		"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/" + id,
		"body": map[string]any{"attachment-": "replacement_b.txt"},
	}}}
	removed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", removeRequest, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, removed, http.StatusOK)
	listed = publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections/batch_documents/records/"+id+"/files/attachment", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	decodePublicUsersJSON(t, listed, &files)
	if len(files.Items) != 2 || files.Items[0]["filename"] != "prepended.txt" || files.Items[1]["filename"] != "appended.txt" {
		t.Fatalf("modifier file order=%#v", files.Items)
	}
	failedRemoval := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []map[string]any{
		{"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/" + id, "body": map[string]any{"attachment-": "prepended.txt"}},
		{"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/019551d0-e9c0-7000-8000-000000000197", "body": map[string]any{"title": "missing"}},
	}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, failedRemoval, http.StatusNotFound)
	listed = publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections/batch_documents/records/"+id+"/files/attachment", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	decodePublicUsersJSON(t, listed, &files)
	if len(files.Items) != 2 || files.Items[0]["filename"] != "prepended.txt" || files.Items[1]["filename"] != "appended.txt" {
		t.Fatalf("failed removal changed files=%#v", files.Items)
	}
	conflict := multipartBatchRequest(t, app, cookie, csrf, requests, map[string][]byte{"attachment_0": []byte("different bytes")})
	assertPublicUsersStatus(t, conflict, http.StatusConflict)
	listed = publicUsersRequest(t, app, http.MethodGet, "/api/admin/collections/batch_documents/records/"+id+"/files/attachment", nil, "", cookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	decodePublicUsersJSON(t, listed, &files)
	if len(files.Items) != 2 || files.Items[0]["filename"] != "prepended.txt" || files.Items[1]["filename"] != "appended.txt" {
		t.Fatalf("idempotency conflict changed files: %#v", files.Items)
	}
	rollbackRequests := map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/batch_documents/records", "headers": map[string]string{"Idempotency-Key": "multipart-record-rollback-0001"}, "body": map[string]any{"title": "must rollback", "attachment": map[string]any{"$file": "rollback_0"}}}, {"method": "PATCH", "url": "/api/v1/collections/batch_documents/records/019551d0-e9c0-7000-8000-000000000198", "body": map[string]any{"title": "missing"}}}}
	rolledBack := multipartBatchRequest(t, app, cookie, csrf, rollbackRequests, map[string][]byte{"rollback_0": []byte("rollback file")})
	assertPublicUsersStatus(t, rolledBack, http.StatusNotFound)
	page := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/batch_documents/records", nil, "", cookie, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	for _, item := range listing.Items {
		if item["title"] == "must rollback" || item["title"] == "missing direct" || item["title"] == "missing batch" {
			t.Fatalf("rolled back record remained: %#v", item)
		}
	}
	var metadata int
	if err := app.app.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.record_files where filename='rollback_0.txt'`).Scan(&metadata); err != nil || metadata != 0 {
		t.Fatalf("rollback metadata=%d err=%v", metadata, err)
	}
}

func TestRequiredFileValidationDoesNotDependOnStorageConfiguration(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "required_without_storage", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file", "required": true}}}
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		Checksum    string `json:"checksum"`
		BaseVersion int64  `json:"baseVersion"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["checksum"], change["baseVersion"] = plan.Checksum, plan.BaseVersion
	applied := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, applied, http.StatusCreated)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/required_without_storage/records", map[string]any{"title": "missing"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "required-no-storage-0001"})
	assertPublicUsersStatus(t, created, http.StatusUnprocessableEntity)
	if !bytes.Contains(created.Body.Bytes(), []byte(`"attachment":["required_file"]`)) {
		t.Fatalf("required-file response=%s", created.Body.String())
	}
}

func multipartBatchRequest(t *testing.T, app *publicUsersFixture, cookie *http.Cookie, csrf string, requests any, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("requests")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(part).Encode(requests); err != nil {
		t.Fatal(err)
	}
	for key, data := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+key+`"; filename="`+key+`.txt"`)
		header.Set("Content-Type", "text/plain")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, app.server.URL+"/api/v1/collections/batch", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "pgbase.test"
	request.Header.Set("Origin", publicUsersOrigin)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-PgBase-CSRF", csrf)
	request.AddCookie(cookie)
	result, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	response := httptest.NewRecorder()
	response.Code = result.StatusCode
	response.HeaderMap = result.Header.Clone()
	_, _ = io.Copy(response.Body, result.Body)
	return response
}

func TestGenericBatchHTTPPutUpsertAndAtomicRollback(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "notes")
	id := "019551d0-e9c0-7000-8000-000000000099"
	put := func(title string) *httptest.ResponseRecorder {
		return publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []map[string]any{{"method": "PUT", "url": "/api/v1/collections/notes/records/" + id, "body": map[string]any{"title": title}}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	}
	first := put("created by put")
	assertPublicUsersStatus(t, first, http.StatusOK)
	second := put("updated by put")
	assertPublicUsersStatus(t, second, http.StatusOK)
	var response struct {
		Results []struct {
			Status  int               `json:"status"`
			Body    map[string]any    `json:"body"`
			Headers map[string]string `json:"headers"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, second, &response)
	if len(response.Results) != 1 || response.Results[0].Status != 200 || response.Results[0].Body["id"] != id || response.Results[0].Headers["ETag"] == "" {
		t.Fatalf("generic upsert response=%#v", response)
	}
	failed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []map[string]any{{"method": "PATCH", "url": "/api/v1/collections/notes/records/" + id, "body": map[string]any{"title": "must rollback"}}, {"method": "PATCH", "url": "/api/v1/collections/notes/records/019551d0-e9c0-7000-8000-000000000098", "body": map[string]any{"title": "missing"}}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, failed, http.StatusNotFound)
	got := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/notes/records/"+id, nil, "", cookie, nil)
	assertPublicUsersStatus(t, got, http.StatusOK)
	var record map[string]any
	decodePublicUsersJSON(t, got, &record)
	if record["title"] != "updated by put" {
		t.Fatalf("rollback record=%#v", record)
	}
}

func TestPublicBatchDenialRollsBackEarlierOperationHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "notes")
	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	first := registerPublicUser(t, app, "batch-first@example.test", publicUserOnePass)
	second := registerPublicUser(t, app, "batch-second@example.test", publicUserTwoPass)
	path := "/api/v1/user/collections/notes/records"
	seed := publicUsersRequest(t, app, http.MethodPost, path, map[string]any{"title": "first private"}, first.Token, nil, map[string]string{"Idempotency-Key": "batch-seed-owner-0001"})
	assertPublicUsersStatus(t, seed, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, seed, &record)
	foreignID := record["id"].(string)

	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "notes", "input": map[string]any{"title": "must roll back"}, "idempotencyKey": "batch-owner-create-0001"},
		{"operation": "update", "collection": "notes", "id": foreignID, "input": map[string]any{"title": "intrusion"}},
	}}, second.Token, nil, nil)
	assertPublicUsersStatus(t, batch, http.StatusNotFound)
	page := publicUsersRequest(t, app, http.MethodGet, path, nil, second.Token, nil, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	if len(listing.Items) != 0 {
		t.Fatalf("denied batch left owned records: %#v", listing.Items)
	}
}

func TestBatchHTTPDeleteHasExplicitResult(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "notes")
	_ = publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	user := registerPublicUser(t, app, "batch-delete@example.test", publicUserOnePass)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/notes/records", map[string]any{"title": "delete"}, user.Token, nil, map[string]string{"Idempotency-Key": "batch-delete-seed-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "delete", "collection": "notes", "id": record["id"]}}}, user.Token, nil, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var body struct {
		Results []struct {
			EventID string `json:"eventId"`
			Record  any    `json:"record"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, response, &body)
	if len(body.Results) != 1 || body.Results[0].EventID == "" || body.Results[0].Record != nil {
		t.Fatalf("delete batch response=%#v", body)
	}
}
