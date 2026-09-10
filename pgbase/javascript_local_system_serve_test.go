package pgbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptLocalSystemServesRootedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("local served"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJavaScriptFixture(t, dir, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/local","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true}]}`, `exports.run=(e)=>$filesystem.local(".").serve(e.response,e.request,"asset.txt","asset.txt")`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: dir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	w := httptest.NewRecorder()
	runtime.routes()[0].Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/local", nil))
	if w.Code != http.StatusOK || w.Body.String() != "local served" {
		t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
	}
}
