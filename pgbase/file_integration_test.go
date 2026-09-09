//go:build integration

package pgbase

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/accessrule"
)

func TestPrivateRecordFileHTTPContract(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
	})
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "documents", "fields": []map[string]any{{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file", "required": false, "options": map[string]any{"maxSelect": 3}}}}
	plan := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	applied := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, applied, http.StatusCreated)
	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/documents/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)

	owner := registerPublicUser(t, app, "file-owner@example.test", publicUserOnePass)
	other := registerPublicUser(t, app, "file-other@example.test", publicUserTwoPass)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/documents/records", map[string]any{"title": "private file"}, owner.Token, nil, map[string]string{"Idempotency-Key": "private-file-record-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	base := "/api/v1/user/collections/documents/records/" + record.ID + "/files/attachment"
	content := []byte("private file contents")
	upload := multipartFileRequest(t, app, base, owner.Token, nil, "note.txt", "text/plain", content, nil)
	assertPublicUsersStatus(t, upload, http.StatusCreated)
	var file struct {
		ID, Filename string
		Size         int64
		Thumbnail    bool
	}
	decodePublicUsersJSON(t, upload, &file)
	if file.ID == "" || file.Filename != "note.txt" || file.Size != int64(len(content)) || file.Thumbnail {
		t.Fatalf("file response = %+v", file)
	}
	adminUploadWithoutCSRF := multipartFileRequest(t, app, "/api/admin/collections/documents/records/"+record.ID+"/files/attachment", "", adminCookie, "admin.txt", "text/plain", content, nil)
	assertPublicUsersStatus(t, adminUploadWithoutCSRF, http.StatusForbidden)
	badDigest := multipartFileRequest(t, app, base, owner.Token, nil, "bad.txt", "text/plain", content, map[string]string{"X-PgBase-File-SHA256": hex.EncodeToString(make([]byte, sha256.Size))})
	assertPublicUsersStatus(t, badDigest, http.StatusBadRequest)
	oversized := bytes.Repeat([]byte("x"), 1025)
	tooLarge := multipartFileRequest(t, app, base, owner.Token, nil, "large.txt", "text/plain", oversized, nil)
	assertPublicUsersStatus(t, tooLarge, http.StatusRequestEntityTooLarge)

	foreign := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/files/"+file.ID+"?variant=original&token=invalid", nil, other.Token, nil, nil)
	assertPublicUsersStatus(t, foreign, http.StatusForbidden)
	tokenResponse := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/files/"+file.ID+"/token", map[string]any{"variant": "original"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, tokenResponse, http.StatusOK)
	var token struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, tokenResponse, &token)
	duplicateToken := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/files/"+file.ID+"?token="+url.QueryEscape(token.Token)+"&token="+url.QueryEscape(token.Token), nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, duplicateToken, http.StatusBadRequest)
	foreign = publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/files/"+file.ID+"?variant=original&token="+url.QueryEscape(token.Token), nil, other.Token, nil, nil)
	assertPublicUsersStatus(t, foreign, http.StatusForbidden)
	download := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/files/"+file.ID+"?variant=original&token="+url.QueryEscape(token.Token), nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), content) || download.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("download content=%q type=%q", download.Body.Bytes(), download.Header().Get("Content-Type"))
	}

	adminTokenResponse := publicUsersRequest(t, app, http.MethodPost, "/api/admin/files/"+file.ID+"/token", map[string]any{"variant": "original"}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, adminTokenResponse, http.StatusOK)
	var adminToken struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, adminTokenResponse, &adminToken)
	adminDownload := publicUsersRequest(t, app, http.MethodGet, "/api/admin/files/"+file.ID+"?variant=original&token="+url.QueryEscape(adminToken.Token), nil, "", adminCookie, nil)
	assertPublicUsersStatus(t, adminDownload, http.StatusOK)
	if !bytes.Equal(adminDownload.Body.Bytes(), content) {
		t.Fatal("administrator download changed file bytes")
	}

	rules := accessrule.OwnerRules()
	rules.View = accessrule.Rule{Op: "allow"}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/documents/rules", map[string]any{"rules": rules}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	sharedTokenResponse := publicUsersRequest(t, app, "POST", "/api/v1/user/files/"+file.ID+"/token", map[string]any{"variant": "original"}, other.Token, nil, nil)
	assertPublicUsersStatus(t, sharedTokenResponse, 200)
	var sharedToken struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, sharedTokenResponse, &sharedToken)
	sharedURL := "/api/v1/user/files/" + file.ID + "?variant=original&token=" + url.QueryEscape(sharedToken.Token)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "GET", sharedURL, nil, other.Token, nil, nil), 200)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "DELETE", "/api/v1/user/files/"+file.ID, nil, other.Token, nil, nil), 404)
	rules.View = accessrule.Rule{Op: "deny"}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/documents/rules", map[string]any{"rules": rules}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "GET", sharedURL, nil, other.Token, nil, nil), 404)
	rules = accessrule.OwnerRules()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/documents/rules", map[string]any{"rules": rules}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	deleted := publicUsersRequest(t, app, http.MethodDelete, "/api/v1/user/files/"+file.ID, nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, deleted, http.StatusNoContent)
	afterDelete := publicUsersRequest(t, app, http.MethodGet, base, nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, afterDelete, http.StatusOK)
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	decodePublicUsersJSON(t, afterDelete, &page)
	if len(page.Items) != 0 {
		t.Fatalf("deleted file remained listed: %s", page.Items)
	}
}

func TestHiddenFileFieldRevokesExistingUserTokens(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	create := map[string]any{"operation": "create", "name": "hidden_files", "fields": []map[string]any{{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file"}}}
	applyFieldVisibilityCollection(t, app, cookie, csrf, create)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/hidden_files/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	owner := registerPublicUser(t, app, "hidden-file-owner@example.test", publicUserOnePass)
	created := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/hidden_files/records", map[string]any{"title": "private"}, owner.Token, nil, map[string]string{"Idempotency-Key": "hidden-file-record-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	base := "/api/v1/user/collections/hidden_files/records/" + record.ID + "/files/attachment"
	content := []byte("still stored")
	upload := multipartFileRequest(t, app, base, owner.Token, nil, "secret.txt", "text/plain", content, nil)
	assertPublicUsersStatus(t, upload, http.StatusCreated)
	var file struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, upload, &file)
	tokenResponse := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/files/"+file.ID+"/token", map[string]any{"variant": "original"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, tokenResponse, http.StatusOK)
	var token struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, tokenResponse, &token)
	userOpen := "/api/v1/user/files/" + file.ID + "?variant=original&token=" + url.QueryEscape(token.Token)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, userOpen, nil, owner.Token, nil, nil), http.StatusOK)
	update := map[string]any{"operation": "update", "name": "hidden_files", "fields": []map[string]any{{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file", "hidden": true}}}
	applyFieldVisibilityCollection(t, app, cookie, csrf, update)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, base, nil, owner.Token, nil, nil), http.StatusNotFound)
	assertPublicUsersStatus(t, multipartFileRequest(t, app, base, owner.Token, nil, "blocked.txt", "text/plain", content, nil), http.StatusNotFound)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/files/"+file.ID+"/token", map[string]any{"variant": "original"}, owner.Token, nil, nil), http.StatusNotFound)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, userOpen, nil, owner.Token, nil, nil), http.StatusNotFound)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodDelete, "/api/v1/user/files/"+file.ID, nil, owner.Token, nil, nil), http.StatusNotFound)
	adminList := "/api/admin/collections/hidden_files/records/" + record.ID + "/files/attachment"
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, adminList, nil, "", cookie, nil), http.StatusOK)
	adminTokenResponse := publicUsersRequest(t, app, http.MethodPost, "/api/admin/files/"+file.ID+"/token", map[string]any{"variant": "original"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, adminTokenResponse, http.StatusOK)
	decodePublicUsersJSON(t, adminTokenResponse, &token)
	adminOpen := "/api/admin/files/" + file.ID + "?variant=original&token=" + url.QueryEscape(token.Token)
	adminDownload := publicUsersRequest(t, app, http.MethodGet, adminOpen, nil, "", cookie, nil)
	assertPublicUsersStatus(t, adminDownload, http.StatusOK)
	if !bytes.Equal(adminDownload.Body.Bytes(), content) {
		t.Fatal("administrator download bytes changed")
	}
}

func multipartFileRequest(t *testing.T, app *publicUsersFixture, path, bearer string, cookie *http.Cookie, filename, contentType string, content []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	partHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, app.server.URL+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "pgbase.test"
	request.Header.Set("Origin", publicUsersOrigin)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+bearer)
	digest := sha256.Sum256(content)
	request.Header.Set("X-PgBase-File-Size", strconv.Itoa(len(content)))
	request.Header.Set("X-PgBase-File-SHA256", hex.EncodeToString(digest[:]))
	if cookie != nil {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
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
