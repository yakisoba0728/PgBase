package pgbase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

func TestExtensionCompatibilityCreatesNestedDirectoriesAndFilesFromHTTP(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("downloaded"))
	}))
	defer server.Close()
	adapter := newExtensionCompatibility(PocketBaseCompatibilityOptions{FilesystemRoot: root, HTTPAllowedOrigins: []string{server.URL}})
	if _, err := adapter.call(context.Background(), "os.mkdirAll", json.RawMessage(`{"path":"one/two/three"}`)); err != nil {
		t.Fatalf("nested mkdir: %v", err)
	}
	if _, err := os.ReadDir(filepath.Join(root, "one", "two", "three")); err != nil {
		t.Fatalf("nested directory missing: %v", err)
	}
	raw, err := adapter.call(context.Background(), "filesystem.fileFromURL", json.RawMessage(`{"url":`+strconvQuote(server.URL)+`}`))
	if err != nil {
		t.Fatalf("file from URL: %v", err)
	}
	var file struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(file.Data)
	if err != nil || string(data) != "downloaded" {
		t.Fatalf("file data=%q error=%v", data, err)
	}
}

func TestExtensionCompatibilityFileFromURLPreservesBinaryAndEmptyBodies(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "binary", body: []byte{0xff, 0x00, 0x80, 0x41}},
		{name: "empty", body: []byte{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			adapter := newExtensionCompatibility(PocketBaseCompatibilityOptions{HTTPAllowedOrigins: []string{server.URL}})
			raw, err := adapter.call(context.Background(), "filesystem.fileFromURL", json.RawMessage(`{"url":`+strconvQuote(server.URL)+`}`))
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatal(err)
			}
			got, err := base64.StdEncoding.DecodeString(file.Data)
			if err != nil || !bytes.Equal(got, test.body) {
				t.Fatalf("file bytes=%x want=%x error=%v", got, test.body, err)
			}
		})
	}
}

func TestExtensionCompatibilityHTTPSendKeepsDocumentedBodyRepresentation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	defer server.Close()
	adapter := newExtensionCompatibility(PocketBaseCompatibilityOptions{HTTPAllowedOrigins: []string{server.URL}})
	raw, err := adapter.call(context.Background(), "http.send", json.RawMessage(`{"url":`+strconvQuote(server.URL)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Body []int  `json:"body"`
		Raw  string `json:"raw"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Body) != 4 || string([]byte{byte(response.Body[0]), byte(response.Body[1]), byte(response.Body[2]), byte(response.Body[3])}) != "pong" || response.Raw != "pong" {
		t.Fatalf("HTTP response = %#v", response)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

type shortReadCloser struct{ calls int }

func (reader *shortReadCloser) Read(buffer []byte) (int, error) {
	reader.calls++
	if reader.calls == 1 {
		return copy(buffer, "ab"), nil
	}
	return copy(buffer, "cd"), io.EOF
}

func (*shortReadCloser) Close() error { return nil }

func TestExtensionObjectChunkDoesNotTreatShortReadAsEOF(t *testing.T) {
	id := uuid.New()
	host := &extensionHost{objectHandles: map[uuid.UUID]io.ReadCloser{id: &shortReadCloser{}}}
	read := func() struct {
		Data string `json:"data"`
		EOF  bool   `json:"eof"`
	} {
		raw, err := host.readObjectChunk(json.RawMessage(`{"handle":"` + id.String() + `","maximumBytes":4}`))
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Data string `json:"data"`
			EOF  bool   `json:"eof"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if first := read(); first.EOF || first.Data != base64.StdEncoding.EncodeToString([]byte("ab")) {
		t.Fatalf("first chunk = %#v", first)
	}
	if second := read(); !second.EOF || second.Data != base64.StdEncoding.EncodeToString([]byte("cd")) {
		t.Fatalf("second chunk = %#v", second)
	}
}

func TestJavaScriptMailPatchPreservesOmittedFields(t *testing.T) {
	message := mailer.Message{Purpose: "verification", To: "person@example.test", Subject: "old", Text: "text", HTML: "<p>html</p>", Attachments: []mailer.Attachment{{Filename: "proof.txt", Data: []byte("proof")}}}
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookMailSend, Data: &message}, json.RawMessage(`{"subject":"new"}`)); err != nil {
		t.Fatal(err)
	}
	if message.Subject != "new" || message.To != "person@example.test" || message.Text != "text" || message.HTML != "<p>html</p>" || message.Purpose != "verification" || len(message.Attachments) != 1 {
		t.Fatalf("message patch = %#v", message)
	}
	event := &MailEvent{Purpose: "otp", Message: message, Meta: map[string]any{"otp": "123456"}}
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookMailOTP, Data: event}, json.RawMessage(`{"message":{"subject":"otp subject"}}`)); err != nil {
		t.Fatal(err)
	}
	if event.Message.Subject != "otp subject" || event.Message.To != "person@example.test" || event.Message.Text != "text" || event.Message.HTML != "<p>html</p>" || event.Message.Purpose != "verification" || event.Purpose != "otp" || len(event.Message.Attachments) != 1 || event.Meta["otp"] != "123456" {
		t.Fatalf("mail event patch = %#v", event)
	}
}

func TestExtensionLogRejectsReservedRequestID(t *testing.T) {
	host := &extensionHost{requestID: "trusted-request"}
	if _, err := host.callLog(context.Background(), "log.info", json.RawMessage(`{"message":"event","fields":{"request_id":"spoofed"}}`)); err == nil {
		t.Fatal("reserved request ID field was accepted")
	}
}

func TestOptionsNormalizedOwnsPublicURL(t *testing.T) {
	publicURL, err := url.Parse("https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	options, err := (Options{DatabaseURL: "postgres://postgres:postgres@localhost/pgbase", TokenSecret: make([]byte, 32), PublicURL: publicURL}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	publicURL.Host = "mutated.example.test"
	if options.PublicURL.Host != "admin.example.test" {
		t.Fatalf("normalized public URL aliases caller: %q", options.PublicURL.Host)
	}
}

func TestJavaScriptRouteBodyClassifiesChunkedMultipartLimit(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/upload", io.NopCloser(bytes.NewBufferString("--boundary\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n123456\r\n--boundary--\r\n")))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	request.ContentLength = -1
	body := &javaScriptRouteBody{request: request, response: httptest.NewRecorder(), maximum: 4, contentType: "multipart/form-data"}
	if _, err := body.read(context.Background(), 0); err != errJavaScriptRequestTooLarge {
		t.Fatalf("multipart limit error = %v", err)
	}
}
