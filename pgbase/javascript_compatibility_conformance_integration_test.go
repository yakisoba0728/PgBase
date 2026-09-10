//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPocketBaseHTTPSendReturnsPublicResponseShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("X-Multi", "one")
		w.Header().Add("X-Multi", "two")
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	directory := t.TempDir()
	// The source is static and receives its URL through a generated module to
	// avoid relying on process environment access for this conformance check.
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/http-shape","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["http"]}]}`, `exports.run=()=>{const r=$http.send({url:`+"`"+upstream.URL+"`"+`});return {status:200,json:{statusCode:r.statusCode,header:r.headers["X-Multi"],cookie:r.cookies&&r.cookies.session&&r.cookies.session.value,json:r.json,body:Array.from(r.body||[]),raw:r.raw}}}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"http": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{HTTPAllowedOrigins: []string{upstream.URL}}}
	})
	response := publicUsersRequest(t, fixture, http.MethodGet, "/http-shape", nil, "", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"body":[123,34,111,107,34,58,116,114,117,101,125],"cookie":"abc","header":["one","two"],"json":{"ok":true},"raw":"{\"ok\":true}","statusCode":200}` {
		t.Fatalf("http shape response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPocketBaseFilesystemConstructorsReachObjectStorage(t *testing.T) {
	directory, files := t.TempDir(), t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/construct","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`, `exports.run=()=>{const f=$filesystem.fileFromBytes([97,108,112,104,97],"alpha.txt");$filesystem.write("from-constructor",f);return {status:200,json:$filesystem.open("from-constructor").read()}}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}}
	})
	response := publicUsersRequest(t, fixture, http.MethodPost, "/construct", map[string]any{}, "", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"data":"YWxwaGE=","eof":true}` {
		t.Fatalf("constructor storage response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPocketBaseStaticIndexFallbackUsesPublicWrapper(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "public")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("fallback"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/spa/{path...}","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true}]}`, `exports.run=$apis.static("public",true)`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: directory}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	request := httptest.NewRequest(http.MethodGet, "/spa/missing/route", nil)
	request.SetPathValue("path", "missing/route")
	response := httptest.NewRecorder()
	runtime.routes()[0].Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "fallback" {
		t.Fatalf("static fallback response=%d body=%q", response.Code, response.Body.String())
	}
}
