//go:build restore

package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"testing"
)

const restoredFileContent = "file objects and metadata must cross the same backup boundary"

func seedRestoreFile(t *testing.T, ctx context.Context, client *adminClient) string {
	t.Helper()
	client.applyCollection(t, ctx, map[string]any{"name": "restore_files", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"}}})
	record := client.createRecord(t, ctx, "restore_files", "restore-file-record-key", map[string]any{"title": "file restore sentinel"})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	headers := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="file"; filename="restore.txt"`}, "Content-Type": {"text/plain"}}
	part, err := writer.CreatePart(headers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, restoredFileContent); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", client.baseURL+"/api/admin/collections/restore_files/records/"+record.id+"/files/attachment", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", client.baseURL)
	request.Header.Set("X-PgBase-CSRF", client.csrf)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-PgBase-File-Size", strconv.Itoa(len(restoredFileContent)))
	hash := sha256.Sum256([]byte(restoredFileContent))
	request.Header.Set("X-PgBase-File-SHA256", hex.EncodeToString(hash[:]))
	response, err := client.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("upload status=%d", response.StatusCode)
	}
	var file struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&file); err != nil {
		t.Fatal(err)
	}
	if file.ID == "" {
		t.Fatal("missing uploaded file identity")
	}
	return file.ID
}

func verifyRestoreFile(t *testing.T, ctx context.Context, client *adminClient, fileID string) {
	t.Helper()
	var token struct {
		Token string `json:"token"`
	}
	client.call(t, ctx, "POST", "/api/admin/files/"+fileID+"/token", map[string]any{"variant": "original"}, nil, 200, &token)
	result, err := client.exchange(ctx, "GET", "/api/admin/files/"+fileID+"?variant=original&token="+url.QueryEscape(token.Token), nil, nil)
	if err != nil || result.status != 200 || string(result.body) != restoredFileContent {
		t.Fatalf("restored file status=%d bytes=%d error=%v", result.status, len(result.body), err)
	}
	if result.header.Get("Content-Type") != "text/plain" {
		t.Fatal("restored file MIME metadata changed")
	}
}
