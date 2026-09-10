//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptFilesystemStreamsAreNamespaceScoped(t *testing.T) {
	dir, files := t.TempDir(), t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/fs","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`
	source := `exports.run=()=>{ return {status:200,json:{write:$filesystem.write("dir/a","YWxwaGE="),copy:$filesystem.copy("dir/a","dir/b")}}}`
	if e := os.WriteFile(filepath.Join(dir, "pgbase.scripts.json"), []byte(manifest), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "route.js"), []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	f := newPublicUsersAppOptions(t, false, func(o *Options) {
		o.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
		o.JavaScript = &JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"filesystem": true}}
	})
	r := publicUsersRequest(t, f, http.MethodPost, "/fs", map[string]any{}, "", nil, nil)
	var out map[string]any
	if r.Code != 200 {
		t.Fatalf("response=%d body=%s", r.Code, r.Body.String())
	}
	decodePublicUsersJSON(t, r, &out)
	if r.Code != 200 || out["copy"].(map[string]any)["size"].(float64) != 5 {
		t.Fatalf("response=%d %#v", r.Code, out)
	}
}
