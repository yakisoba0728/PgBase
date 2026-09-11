//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJavaScriptFilesystemSystemServeAndCreateThumb(t *testing.T) {
	runJavaScriptFilesystemSystemServeAndCreateThumb(t, false)
}

func TestJavaScriptFilesystemSystemServeAndCreateThumbOnS3(t *testing.T) {
	runJavaScriptFilesystemSystemServeAndCreateThumb(t, true)
}

func runJavaScriptFilesystemSystemServeAndCreateThumb(t *testing.T, s3 bool) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[
 {"method":"GET","pattern":"/extensions/system/file","access":"public","handler":{"module":"route.js","export":"serve"},"capabilities":["filesystem"],"streamResponse":true},
 {"method":"HEAD","pattern":"/extensions/system/file","access":"public","handler":{"module":"route.js","export":"serve"},"capabilities":["filesystem"],"streamResponse":true},
 {"method":"POST","pattern":"/extensions/system/thumb","access":"public","handler":{"module":"route.js","export":"thumb"},"capabilities":["filesystem"]}
]}`
	source := `const fs=$filesystem.s3("ignored","ignored","ignored","ignored","ignored");
exports.serve=(e)=>{fs.serve(e.response,e.request,e.query.thumb?"image-thumb.png":"asset.txt",e.query.thumb?"image-thumb.png":"asset.txt")};
exports.thumb=()=>{fs.createThumb("image.png","image-thumb.png","1x1");return {status:204}};`
	for name, content := range map[string]string{"pgbase.scripts.json": manifest, "route.js": source} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain", "image/png"}}
		if s3 {
			options.Files = FileOptions{Driver: "s3", S3Bucket: "pgbase-test", S3Region: "us-east-1", S3Endpoint: testS3Endpoint(t), S3AccessKey: "pgbase-test-access", S3SecretKey: "pgbase-test-secret-0123456789", S3UsePathStyle: true, S3AllowInsecureLoopback: true, MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain", "image/png"}}
		}
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}, Concurrency: 1, Queue: 1}
	})
	objects := fixture.app.extensions.objects
	if err := objects.Write(context.Background(), "route.js", "asset.txt", []byte("served bytes")); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&encoded, imageValue); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write(context.Background(), "route.js", "image.png", encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	physical, err := objects.physicalKey(context.Background(), "route.js", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.extensions.files.RenderThumbnail(context.Background(), physical, "image/png", "1x1"); err != nil {
		t.Fatalf("native thumbnail pipeline: %v", err)
	}

	plain := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/system/file", nil, "", nil, nil)
	assertPublicUsersStatus(t, plain, http.StatusOK)
	if plain.Body.String() != "served bytes" || plain.Header().Get("Content-Type") != "text/plain" || plain.Header().Get("Content-Disposition") == "" {
		t.Fatalf("serve response=%d headers=%v body=%q", plain.Code, plain.Header(), plain.Body.String())
	}
	ranged := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/system/file", nil, "", nil, map[string]string{"Range": "bytes=0-5"})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "served" {
		t.Fatalf("range=%d body=%q", ranged.Code, ranged.Body.String())
	}
	head := publicUsersRequest(t, fixture, http.MethodHead, "/extensions/system/file", nil, "", nil, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", head.Code, head.Body.String())
	}
	thumb := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/system/thumb", nil, "", nil, nil)
	assertPublicUsersStatus(t, thumb, http.StatusNoContent)
	preview := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/system/file?thumb=1", nil, "", nil, nil)
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "image/png" || preview.Body.Len() == 0 {
		t.Fatalf("thumbnail=%d type=%q size=%d", preview.Code, preview.Header().Get("Content-Type"), preview.Body.Len())
	}
}
