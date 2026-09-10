package pgbase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtensionCompatibilityUsesBoundedConfiguredAdapters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("pong")) }))
	defer server.Close()
	root := t.TempDir()
	exitCode := -1
	command := "/bin/echo"
	if runtime.GOOS == "windows" {
		t.Skip("command fixture unavailable")
	}
	options, err := (PocketBaseCompatibilityOptions{FilesystemRoot: root, HTTPAllowedOrigins: []string{server.URL}, Commands: map[string]string{"echo": command}, Environment: map[string]string{"VISIBLE": "yes"}, Arguments: []string{"serve"}, ProcessExit: func(code int) error { exitCode = code; return nil }}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	adapter := newExtensionCompatibility(options)
	call := func(method, input string) json.RawMessage {
		t.Helper()
		out, err := adapter.call(context.Background(), method, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return out
	}
	data := base64.StdEncoding.EncodeToString([]byte("hello"))
	call("os.writeFile", `{"Path":"a.txt","Data":"`+data+`"}`)
	if got, err := os.ReadFile(filepath.Join(root, "a.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("file=%q err=%v", got, err)
	}
	if out := call("http.send", `{"URL":"`+server.URL+`"}`); !json.Valid(out) {
		t.Fatalf("http=%s", out)
	}
	if out := call("os.cmd", `{"name":"echo","args":["bounded"]}`); !json.Valid(out) {
		t.Fatalf("command=%s", out)
	}
	call("os.exit", `{"code":7}`)
	if exitCode != 7 {
		t.Fatalf("exit=%d", exitCode)
	}
	if _, err := adapter.call(context.Background(), "os.readFile", json.RawMessage(`{"path":"../escape"}`)); err == nil {
		t.Fatal("path escape accepted")
	}
	if _, err := adapter.call(context.Background(), "http.send", json.RawMessage(`{"url":"https://example.com"}`)); err == nil {
		t.Fatal("unlisted origin accepted")
	}
}

func TestExtensionCompatibilityProcessControlDefaultsToDenied(t *testing.T) {
	adapter := newExtensionCompatibility(PocketBaseCompatibilityOptions{})
	if _, err := adapter.call(context.Background(), "os.exit", json.RawMessage(`{"code":0}`)); err == nil {
		t.Fatal("process exit accepted without callback")
	}
}

func TestExtensionCompatibilityGlobIsRooted(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	options, err := (PocketBaseCompatibilityOptions{FilesystemRoot: root}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	adapter := newExtensionCompatibility(options)
	raw, err := adapter.call(context.Background(), "os.glob", json.RawMessage(`{"path":"*.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil || len(got) != 1 || got[0] != "one.txt" {
		t.Fatalf("glob=%s err=%v", raw, err)
	}
	for _, pattern := range []string{"../*.txt", "escape/*.txt"} {
		if _, err := adapter.call(context.Background(), "os.glob", json.RawMessage(`{"path":"`+pattern+`"}`)); err == nil {
			t.Fatalf("glob %q escaped root", pattern)
		}
	}
}
