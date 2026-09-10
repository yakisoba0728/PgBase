//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptLocalSystemServesRootedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("local served"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/extensions/local/file","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true},{"method":"HEAD","pattern":"/extensions/local/file","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "route.js"), []byte(`exports.run=(e)=>$filesystem.local(".").serve(e.response,e.request,"asset.txt","asset.txt")`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: dir}}
	})
	full := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/local/file", nil, "", nil, nil)
	assertPublicUsersStatus(t, full, http.StatusOK)
	if full.Body.String() != "local served" {
		t.Fatalf("body=%q", full.Body.String())
	}
	ranged := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/local/file", nil, "", nil, map[string]string{"Range": "bytes=0-4"})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "local" {
		t.Fatalf("range=%d body=%q", ranged.Code, ranged.Body.String())
	}
	head := publicUsersRequest(t, fixture, http.MethodHead, "/extensions/local/file", nil, "", nil, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", head.Code, head.Body.String())
	}
}
