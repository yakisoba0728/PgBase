//go:build integration

package pgbase

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJavaScriptLocalSystemServesRootedFile(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	files := filepath.Join(root, "assets")
	if err := os.Mkdir(files, 0o700); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(files, "asset.txt"), []byte("local served"), 0o600); err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	value := image.NewRGBA(image.Rect(0, 0, 4, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&source, value); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "image.png"), source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(external, "outside.txt"), filepath.Join(files, "outside.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(external, "thumb.png"), filepath.Join(files, "escape-thumb.png")); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/extensions/local/file","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true},{"method":"HEAD","pattern":"/extensions/local/file","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"],"streamResponse":true},{"method":"POST","pattern":"/extensions/local/thumb","access":"public","handler":{"module":"route.js","export":"thumb"},"capabilities":["filesystem"]},{"method":"GET","pattern":"/extensions/local/bad-serve","access":"public","handler":{"module":"route.js","export":"badServe"},"capabilities":["filesystem"],"streamResponse":true},{"method":"POST","pattern":"/extensions/local/bad-thumb","access":"public","handler":{"module":"route.js","export":"badThumb"},"capabilities":["filesystem"]},{"method":"POST","pattern":"/extensions/local/symlink-thumb","access":"public","handler":{"module":"route.js","export":"symlinkThumb"},"capabilities":["filesystem"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "route.js"), []byte(`const fs=()=>$filesystem.local("assets");exports.run=(e)=>fs().serve(e.response,e.request,e.query.thumb?"image-thumb.png":"asset.txt",e.query.thumb?"image-thumb.png":"asset.txt");exports.thumb=()=>{fs().createThumb("image.png","image-thumb.png","1x1");return {status:204}};exports.badServe=(e)=>fs().serve(e.response,e.request,"../asset.txt","asset.txt");exports.badThumb=()=>{fs().createThumb("../image.png","image-thumb.png","1x1");return {status:204}};exports.symlinkThumb=()=>{fs().createThumb("image.png","escape-thumb.png","1x1");return {status:204}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain", "image/png"}}
		options.JavaScript = &JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: root}}
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
	created := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/local/thumb", nil, "", nil, nil)
	assertPublicUsersStatus(t, created, http.StatusNoContent)
	thumbnail := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/local/file?thumb=1", nil, "", nil, nil)
	assertPublicUsersStatus(t, thumbnail, http.StatusOK)
	config, format, err := image.DecodeConfig(bytes.NewReader(thumbnail.Body.Bytes()))
	if err != nil || format != "png" || config.Width != 1 || config.Height != 1 {
		t.Fatalf("thumbnail format=%q dimensions=%dx%d err=%v", format, config.Width, config.Height, err)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/extensions/local/bad-serve"},
		{http.MethodPost, "/extensions/local/bad-thumb"},
		{http.MethodPost, "/extensions/local/symlink-thumb"},
	} {
		response := publicUsersRequest(t, fixture, request.method, request.path, nil, "", nil, nil)
		if response.Code < http.StatusInternalServerError {
			t.Fatalf("%s %s status=%d", request.method, request.path, response.Code)
		}
	}
}
