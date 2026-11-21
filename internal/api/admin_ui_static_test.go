package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	adminui "github.com/yakisoba0728/PgBase/ui/admin"
)

// TestRouterServesPrecompressedAdminAssetsWithoutDoubleCompression runs the
// real embedded UI behind the full middleware chain. The compression
// middleware must recognise the handler's own Content-Encoding and pass the
// precompressed bytes through untouched.
func TestRouterServesPrecompressedAdminAssetsWithoutDoubleCompression(t *testing.T) {
	ui, err := adminui.Handler()
	if err != nil {
		t.Fatal(err)
	}
	handler := mustRouter(t, Dependencies{AdminUI: ui})
	entries, err := fs.Glob(adminui.Dist, "dist/assets/*.js")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded scripts: %v %v", entries, err)
	}
	var name string
	var original []byte
	for _, entry := range entries {
		contents, err := fs.ReadFile(adminui.Dist, entry)
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) > len(original) {
			name, original = entry, contents
		}
	}
	assetPath := "/admin/" + strings.TrimPrefix(name, "dist/")
	serve := func(method string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, assetPath, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	gzipped := serve(http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})
	header := gzipped.Header()
	if gzipped.Code != http.StatusOK || len(header.Values("Content-Encoding")) != 1 || header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("status=%d headers=%v", gzipped.Code, header)
	}
	if got := header.Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding" {
		t.Fatalf("Vary=%q", got)
	}
	if header.Get("Content-Length") != strconv.Itoa(gzipped.Body.Len()) || !strings.HasSuffix(header.Get("ETag"), `-gzip"`) {
		t.Fatalf("headers=%v body=%d", header, gzipped.Body.Len())
	}
	// Exactly one gzip layer: decoding once yields the embedded bytes.
	reader, err := gzip.NewReader(bytes.NewReader(gzipped.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(decoded, original) {
		t.Fatalf("decoded %d bytes (err %v), want the %d embedded bytes", len(decoded), err, len(original))
	}

	// An identity response of a compressible type stays plain for a client
	// that refuses gzip and carries Vary once.
	identity := serve(http.MethodGet, map[string]string{"Accept-Encoding": "gzip;q=0"})
	if identity.Code != http.StatusOK || identity.Header().Get("Content-Encoding") != "" || !bytes.Equal(identity.Body.Bytes(), original) {
		t.Fatalf("identity status=%d headers=%v", identity.Code, identity.Header())
	}
	if got := identity.Header().Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding" {
		t.Fatalf("identity Vary=%q", got)
	}

	notModified := serve(http.MethodGet, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": header.Get("ETag")})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 || notModified.Header().Get("Content-Encoding") != "" {
		t.Fatalf("304 status=%d headers=%v", notModified.Code, notModified.Header())
	}

	ranged := serve(http.MethodGet, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-99"})
	if ranged.Code != http.StatusPartialContent || ranged.Header().Get("Content-Encoding") != "" || !bytes.Equal(ranged.Body.Bytes(), original[:100]) {
		t.Fatalf("range status=%d headers=%v", ranged.Code, ranged.Header())
	}

	head := serve(http.MethodHead, map[string]string{"Accept-Encoding": "gzip"})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "gzip" ||
		head.Header().Get("Content-Length") != strconv.Itoa(gzipped.Body.Len()) {
		t.Fatalf("HEAD status=%d headers=%v", head.Code, head.Header())
	}

	index := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	index.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, index)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("ETag") == "" ||
		len(response.Header().Values("Content-Encoding")) > 1 {
		t.Fatalf("index status=%d headers=%v", response.Code, response.Header())
	}
}

func TestCompressionMiddlewareDoesNotRepeatVary(t *testing.T) {
	body := strings.Repeat("x", 2*minimumCompressedBytes)
	for _, vary := range []string{"Accept-Encoding", "accept-encoding", "Origin, Accept-Encoding", "*"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response := httptest.NewRecorder()
		compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Vary", vary)
			_, _ = w.Write([]byte(body))
		})).ServeHTTP(response, request)
		if got := response.Header().Values("Vary"); len(got) != 1 || got[0] != vary || response.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("Vary %q: got %q headers=%v", vary, got, response.Header())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Vary", "Origin")
		_, _ = w.Write([]byte(body))
	})).ServeHTTP(response, request)
	if got := response.Header().Values("Vary"); len(got) != 2 || got[1] != "Accept-Encoding" {
		t.Fatalf("Vary=%q", got)
	}
}
