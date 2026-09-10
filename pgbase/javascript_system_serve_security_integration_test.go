//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptSystemServeBindsResponseToInvocation(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("bound response"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":"pgbase.scripts/v1","routes":[
	 {"method":"GET","pattern":"/serve","access":"public","handler":{"module":"route.js","export":"serve"},"capabilities":["filesystem"],"streamResponse":true},
	 {"method":"GET","pattern":"/forge","access":"public","handler":{"module":"route.js","export":"forge"},"capabilities":["filesystem"],"streamResponse":true},
	 {"method":"GET","pattern":"/reuse","access":"public","handler":{"module":"route.js","export":"reuse"},"capabilities":["filesystem"],"streamResponse":true},
	 {"method":"GET","pattern":"/leak","access":"public","handler":{"module":"route.js","export":"leak"}},
	 {"method":"POST","pattern":"/stale","access":"public","handler":{"module":"route.js","export":"stale"},"capabilities":["filesystem"],"streamResponse":true}
	]}`
	source := `exports.serve=(e)=>{const result=$filesystem.local(".").serve(e.response,e.request,"asset.txt","asset.txt");if(result!==undefined)throw new Error("serve must return undefined")};
	exports.forge=(e)=>$filesystem.local(".").serve({},e.request,"asset.txt","asset.txt");
	exports.reuse=(e)=>{const fs=$filesystem.local(".");fs.serve(e.response,e.request,"asset.txt","asset.txt");fs.serve(e.response,e.request,"asset.txt","asset.txt")};
	exports.leak=(e)=>({status:200,json:{request:e.request,response:e.response}});
	exports.stale=(e)=>$filesystem.local(".").serve(e.json.response,e.json.request,"asset.txt","asset.txt");`
	writeJavaScriptFixture(t, directory, manifest, source)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: directory}}
	})
	normal := publicUsersRequest(t, fixture, http.MethodGet, "/serve", nil, "", nil, nil)
	if normal.Code != http.StatusOK || normal.Body.String() != "bound response" {
		t.Fatalf("normal=%d body=%q", normal.Code, normal.Body.String())
	}
	for _, path := range []string{"/forge", "/reuse"} {
		response := publicUsersRequest(t, fixture, http.MethodGet, path, nil, "", nil, nil)
		if response.Code < 400 {
			t.Fatalf("%s accepted handle: %d %s", path, response.Code, response.Body.String())
		}
	}
	leak := publicUsersRequest(t, fixture, http.MethodGet, "/leak", nil, "", nil, nil)
	if leak.Code != http.StatusOK || leak.Body.String() != `{"request":{},"response":{}}` {
		t.Fatalf("opaque handles leaked: %d %s", leak.Code, leak.Body.String())
	}
	stale := publicUsersRequest(t, fixture, http.MethodPost, "/stale", map[string]any{"request": map[string]any{}, "response": map[string]any{}}, "", nil, nil)
	if stale.Code < 400 {
		t.Fatalf("serialized prior-invocation handles accepted: %d %s", stale.Code, stale.Body.String())
	}
}
