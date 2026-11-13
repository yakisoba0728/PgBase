package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestNativeJavaScriptHostUsesRootedFilesAndOperatorAllowLists(t *testing.T) {
	directory, root := t.TempDir(), t.TempDir()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("native")) }))
	defer upstream.Close()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v2","routes":[{"method":"POST","pattern":"/native-host","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem","os","http"]}]}`, `
const pg=require('pgbase');
if(typeof $app!=='undefined'||typeof $os!=='undefined')throw new Error('legacy globals');
exports.run=input=>{
  pg.filesystem.write({path:'native.bin',dataBase64:'bmF0aXZl'});
  let escaped=false,commandDenied=false,originDenied=false;
  try{pg.filesystem.read({path:'../outside'})}catch(_){escaped=true}
  try{pg.os.command({name:'unapproved'})}catch(_){commandDenied=true}
  try{pg.http.send({url:'http://unapproved.invalid/'})}catch(_){originDenied=true}
  return {data:pg.filesystem.read({path:'native.bin'}).dataBase64,env:pg.os.getenv({name:'VISIBLE'}),escaped,commandDenied,originDenied,response:pg.http.send({url:input.url}).statusCode};
}`)
	options, err := (JavaScriptOptions{Directory: directory, Host: JavaScriptHostOptions{
		FilesystemRoot: root, Environment: map[string]string{"VISIBLE": "approved"}, HTTPAllowedOrigins: []string{upstream.URL},
	}}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, options.limits())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: &extensionBridge{hostPolicy: newExtensionHostPolicy(options.Host)}, valid: true}
	input, _ := json.Marshal(map[string]string{"url": upstream.URL})
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"filesystem", "os", "http"}, Host: host, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data, Env                            string
		Escaped, CommandDenied, OriginDenied bool
		Response                             int
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Data != "bmF0aXZl" || result.Env != "approved" || !result.Escaped || !result.CommandDenied || !result.OriginDenied || result.Response != http.StatusOK {
		t.Fatalf("native host boundary = %s, %v", output, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "native.bin")); err != nil || string(data) != "native" {
		t.Fatalf("native rooted write = %q, %v", data, err)
	}
}

func TestNativeTypeDeclarationsDoNotAdvertiseLegacyGlobals(t *testing.T) {
	// The native declarations must not silently permit code the native VM rejects.
	declarations := JavaScriptTypeDeclarations()
	for _, legacy := range []string{"const $app", "class PBRecord", "function routerAdd", "const $os"} {
		if strings.Contains(declarations, legacy) {
			t.Fatalf("native declarations include %s", legacy)
		}
	}
}
