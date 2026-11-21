package admin

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// largestEmbeddedAsset returns the biggest file of the given extension in the
// tracked build, which is the asset the precompression matters most for.
func largestEmbeddedAsset(t testing.TB, extension string) (string, []byte) {
	t.Helper()
	entries, err := fs.Glob(Dist, "dist/assets/*"+extension)
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded %s assets: entries=%v err=%v", extension, entries, err)
	}
	var name string
	var largest []byte
	for _, entry := range entries {
		contents, err := fs.ReadFile(Dist, entry)
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) > len(largest) {
			name, largest = entry, contents
		}
	}
	return "/admin/" + strings.TrimPrefix(name, "dist/"), largest
}

func smallestEmbeddedAsset(t testing.TB, extension string) (string, []byte) {
	t.Helper()
	entries, err := fs.Glob(Dist, "dist/assets/*"+extension)
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded %s assets: entries=%v err=%v", extension, entries, err)
	}
	var name string
	var smallest []byte
	for _, entry := range entries {
		contents, err := fs.ReadFile(Dist, entry)
		if err != nil {
			t.Fatal(err)
		}
		if name == "" || len(contents) < len(smallest) {
			name, smallest = entry, contents
		}
	}
	return "/admin/" + strings.TrimPrefix(name, "dist/"), smallest
}

type staticRequest struct {
	method  string
	path    string
	headers map[string]string
}

func serveStatic(t testing.TB, handler http.Handler, request staticRequest) *httptest.ResponseRecorder {
	t.Helper()
	method := request.method
	if method == "" {
		method = http.MethodGet
	}
	r := httptest.NewRequest(method, request.path, nil)
	for name, value := range request.headers {
		r.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	return response
}

func gunzip(t testing.TB, body []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip header: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gzip body: %v", err)
	}
	return decoded
}

func varies(header http.Header) bool {
	for _, value := range header.Values("Vary") {
		for _, member := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(member), "Accept-Encoding") {
				return true
			}
		}
	}
	return false
}

func newTestHandler(t testing.TB) http.Handler {
	t.Helper()
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestStaticAssetNegotiatesPrecompressedGzipAndIdentity(t *testing.T) {
	handler := newTestHandler(t)
	assetPath, original := largestEmbeddedAsset(t, ".js")

	gzipped := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Accept-Encoding": "gzip, deflate, br"}})
	header := gzipped.Header()
	if gzipped.Code != http.StatusOK || header.Get("Content-Encoding") != "gzip" || !varies(header) {
		t.Fatalf("gzip status=%d headers=%v", gzipped.Code, header)
	}
	if header.Get("Content-Length") != strconv.Itoa(gzipped.Body.Len()) || gzipped.Body.Len() >= len(original) {
		t.Fatalf("gzip Content-Length=%q body=%d original=%d", header.Get("Content-Length"), gzipped.Body.Len(), len(original))
	}
	if header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("gzip cache/type headers=%v", header)
	}
	if header.Get("Accept-Ranges") != "" {
		t.Fatalf("gzip representation advertised byte ranges: %v", header)
	}
	if header.Get("Content-Security-Policy") == "" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("gzip response lost the security headers: %v", header)
	}
	if !bytes.Equal(gunzip(t, gzipped.Body.Bytes()), original) {
		t.Fatal("decompressed asset differs from the embedded bytes")
	}
	gzipETag := header.Get("ETag")
	if !strings.HasPrefix(gzipETag, `"`) || !strings.HasSuffix(gzipETag, `-gzip"`) {
		t.Fatalf("gzip ETag=%q", gzipETag)
	}

	var identityETag string
	for _, acceptEncoding := range []string{"", "identity", "gzip;q=0", "*;q=0", "gzip;q=0, *", "gzip;q=0.000", "br", "gzip;q=2", "gzip;q=0.5, gzip;q=0"} {
		identity := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Accept-Encoding": acceptEncoding}})
		header := identity.Header()
		if identity.Code != http.StatusOK || header.Get("Content-Encoding") != "" || !varies(header) || !bytes.Equal(identity.Body.Bytes(), original) {
			t.Fatalf("Accept-Encoding %q: status=%d headers=%v body=%d bytes", acceptEncoding, identity.Code, header, identity.Body.Len())
		}
		if header.Get("Content-Length") != strconv.Itoa(len(original)) || header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("Accept-Encoding %q: headers=%v", acceptEncoding, header)
		}
		if identityETag == "" {
			identityETag = header.Get("ETag")
		} else if header.Get("ETag") != identityETag {
			t.Fatalf("identity ETag changed between requests: %q != %q", header.Get("ETag"), identityETag)
		}
	}
	if identityETag == "" || identityETag == gzipETag || strings.HasPrefix(identityETag, "W/") {
		t.Fatalf("identity ETag=%q gzip ETag=%q", identityETag, gzipETag)
	}

	for _, acceptEncoding := range []string{"gzip", "GZIP;q=0.5", "*", "identity, *;q=1", "gzip;q=1.000"} {
		response := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Accept-Encoding": acceptEncoding}})
		if response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("ETag") != gzipETag {
			t.Fatalf("Accept-Encoding %q: headers=%v", acceptEncoding, response.Header())
		}
	}
}

func TestStaticAssetConditionalRequests(t *testing.T) {
	handler := newTestHandler(t)
	assetPath, _ := largestEmbeddedAsset(t, ".css")
	gzipHeaders := map[string]string{"Accept-Encoding": "gzip"}
	gzipETag := serveStatic(t, handler, staticRequest{path: assetPath, headers: gzipHeaders}).Header().Get("ETag")
	identityETag := serveStatic(t, handler, staticRequest{path: assetPath}).Header().Get("ETag")

	notModified := func(label string, response *httptest.ResponseRecorder, etag string) {
		t.Helper()
		header := response.Header()
		if response.Code != http.StatusNotModified || response.Body.Len() != 0 || header.Get("ETag") != etag ||
			header.Get("Content-Encoding") != "" || header.Get("Content-Type") != "" || header.Get("Content-Length") != "" ||
			header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !varies(header) {
			t.Fatalf("%s: status=%d headers=%v body=%d", label, response.Code, header, response.Body.Len())
		}
	}
	for _, ifNoneMatch := range []string{gzipETag, "W/" + gzipETag, `"other", ` + gzipETag, "*"} {
		notModified("gzip If-None-Match "+ifNoneMatch, serveStatic(t, handler, staticRequest{path: assetPath,
			headers: map[string]string{"Accept-Encoding": "gzip", "If-None-Match": ifNoneMatch}}), gzipETag)
		notModified("gzip HEAD If-None-Match "+ifNoneMatch, serveStatic(t, handler, staticRequest{method: http.MethodHead, path: assetPath,
			headers: map[string]string{"Accept-Encoding": "gzip", "If-None-Match": ifNoneMatch}}), gzipETag)
	}
	for _, ifNoneMatch := range []string{identityETag, "W/" + identityETag, `"other", ` + identityETag} {
		notModified("identity If-None-Match "+ifNoneMatch, serveStatic(t, handler, staticRequest{path: assetPath,
			headers: map[string]string{"If-None-Match": ifNoneMatch}}), identityETag)
	}

	// A validator of the other representation does not match.
	response := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Accept-Encoding": "gzip", "If-None-Match": identityETag}})
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip request with identity validator: status=%d headers=%v", response.Code, response.Header())
	}
	response = serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"If-None-Match": gzipETag}})
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "" {
		t.Fatalf("identity request with gzip validator: status=%d headers=%v", response.Code, response.Header())
	}

	// If-Match uses the strong comparison on either representation.
	for _, test := range []struct {
		headers map[string]string
		status  int
	}{
		{map[string]string{"Accept-Encoding": "gzip", "If-Match": gzipETag}, http.StatusOK},
		{map[string]string{"Accept-Encoding": "gzip", "If-Match": "*"}, http.StatusOK},
		{map[string]string{"Accept-Encoding": "gzip", "If-Match": "W/" + gzipETag}, http.StatusPreconditionFailed},
		{map[string]string{"Accept-Encoding": "gzip", "If-Match": identityETag}, http.StatusPreconditionFailed},
		{map[string]string{"If-Match": identityETag}, http.StatusOK},
		{map[string]string{"If-Match": gzipETag}, http.StatusPreconditionFailed},
	} {
		if response := serveStatic(t, handler, staticRequest{path: assetPath, headers: test.headers}); response.Code != test.status {
			t.Fatalf("%v: status=%d, want %d", test.headers, response.Code, test.status)
		}
	}
}

func TestStaticAssetRangeAndHeadUseTheRightRepresentation(t *testing.T) {
	handler := newTestHandler(t)
	assetPath, original := largestEmbeddedAsset(t, ".js")
	identityETag := serveStatic(t, handler, staticRequest{path: assetPath}).Header().Get("ETag")

	// Ranges always address the identity bytes, even for a gzip-capable client.
	for _, headers := range []map[string]string{
		{"Range": "bytes=100-1099"},
		{"Range": "bytes=100-1099", "Accept-Encoding": "gzip"},
		{"Range": "bytes=100-1099", "Accept-Encoding": "gzip", "If-Range": identityETag},
	} {
		response := serveStatic(t, handler, staticRequest{path: assetPath, headers: headers})
		header := response.Header()
		if response.Code != http.StatusPartialContent || header.Get("Content-Encoding") != "" ||
			!bytes.Equal(response.Body.Bytes(), original[100:1100]) ||
			header.Get("Content-Range") != "bytes 100-1099/"+strconv.Itoa(len(original)) || header.Get("ETag") != identityETag {
			t.Fatalf("%v: status=%d headers=%v body=%d", headers, response.Code, header, response.Body.Len())
		}
	}
	// A stale If-Range validator turns the range into the whole identity body.
	response := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Range": "bytes=0-9", "If-Range": `"stale"`}})
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), original) {
		t.Fatalf("stale If-Range: status=%d body=%d", response.Code, response.Body.Len())
	}
	response = serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Range": "bytes=999999999-"}})
	if response.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range: status=%d", response.Code)
	}

	get := serveStatic(t, handler, staticRequest{path: assetPath, headers: map[string]string{"Accept-Encoding": "gzip"}})
	head := serveStatic(t, handler, staticRequest{method: http.MethodHead, path: assetPath, headers: map[string]string{"Accept-Encoding": "gzip"}})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "gzip" ||
		head.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) || head.Header().Get("ETag") != get.Header().Get("ETag") || !varies(head.Header()) {
		t.Fatalf("gzip HEAD status=%d headers=%v body=%d", head.Code, head.Header(), head.Body.Len())
	}
	head = serveStatic(t, handler, staticRequest{method: http.MethodHead, path: assetPath})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "" ||
		head.Header().Get("Content-Length") != strconv.Itoa(len(original)) || head.Header().Get("ETag") != identityETag {
		t.Fatalf("identity HEAD status=%d headers=%v body=%d", head.Code, head.Header(), head.Body.Len())
	}
}

func TestStaticIndexHasValidatorsAndKeepsNoCache(t *testing.T) {
	handler := newTestHandler(t)
	index, err := fs.ReadFile(Dist, "dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, requestPath := range []string{"/admin/", "/admin/collections/posts", "/admin/collections/"} {
		identity := serveStatic(t, handler, staticRequest{path: requestPath})
		etag := identity.Header().Get("ETag")
		if identity.Code != http.StatusOK || !bytes.Equal(identity.Body.Bytes(), index) || etag == "" ||
			identity.Header().Get("Cache-Control") != "no-cache" || identity.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s identity: status=%d headers=%v", requestPath, identity.Code, identity.Header())
		}
		revalidated := serveStatic(t, handler, staticRequest{path: requestPath, headers: map[string]string{"If-None-Match": etag}})
		if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 || revalidated.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s revalidation: status=%d headers=%v", requestPath, revalidated.Code, revalidated.Header())
		}
		gzipped := serveStatic(t, handler, staticRequest{path: requestPath, headers: map[string]string{"Accept-Encoding": "gzip"}})
		if len(index) >= minimumCompressedBytes {
			if gzipped.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(gunzip(t, gzipped.Body.Bytes()), index) || gzipped.Header().Get("ETag") == etag {
				t.Fatalf("%s gzip: headers=%v", requestPath, gzipped.Header())
			}
			revalidated := serveStatic(t, handler, staticRequest{path: requestPath,
				headers: map[string]string{"Accept-Encoding": "gzip", "If-None-Match": gzipped.Header().Get("ETag")}})
			if revalidated.Code != http.StatusNotModified || revalidated.Header().Get("Cache-Control") != "no-cache" {
				t.Fatalf("%s gzip revalidation: status=%d headers=%v", requestPath, revalidated.Code, revalidated.Header())
			}
		}
	}
	// The raw file stays reachable by name with the same no-cache policy.
	raw := serveStatic(t, handler, staticRequest{path: "/admin/index.html"})
	if raw.Code != http.StatusOK || !bytes.Equal(raw.Body.Bytes(), index) || raw.Header().Get("Cache-Control") != "no-cache" || raw.Header().Get("ETag") == "" {
		t.Fatalf("raw index: status=%d headers=%v", raw.Code, raw.Header())
	}
}

func TestStaticIndexValidatorFollowsExtensionLoader(t *testing.T) {
	plain := serveStatic(t, newTestHandler(t), staticRequest{path: "/admin/"})
	extended, err := Handler([]UIExtension{{Name: "reports", FS: fstest.MapFS{"main.js": {Data: []byte("export {}")}}}})
	if err != nil {
		t.Fatal(err)
	}
	withLoader := serveStatic(t, extended, staticRequest{path: "/admin/"})
	if plain.Header().Get("ETag") == withLoader.Header().Get("ETag") || !strings.Contains(withLoader.Body.String(), "/admin/extensions.js") {
		t.Fatalf("index ETag did not change with its body: %q", plain.Header().Get("ETag"))
	}
}

func TestStaticAssetLeavesCompressedFormatsAndSmallFilesPlain(t *testing.T) {
	handler := newTestHandler(t)
	fontPath, font := largestEmbeddedAsset(t, ".woff2")
	response := serveStatic(t, handler, staticRequest{path: fontPath, headers: map[string]string{"Accept-Encoding": "gzip"}})
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "" || varies(response.Header()) ||
		!bytes.Equal(response.Body.Bytes(), font) || response.Header().Get("ETag") == "" ||
		response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("Content-Type") != "font/woff2" {
		t.Fatalf("woff2: status=%d headers=%v", response.Code, response.Header())
	}

	smallPath, small := smallestEmbeddedAsset(t, ".js")
	if len(small) >= minimumCompressedBytes {
		t.Skipf("smallest script is %d bytes", len(small))
	}
	response = serveStatic(t, handler, staticRequest{path: smallPath, headers: map[string]string{"Accept-Encoding": "gzip"}})
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "" || !varies(response.Header()) || !bytes.Equal(response.Body.Bytes(), small) {
		t.Fatalf("small script: status=%d headers=%v", response.Code, response.Header())
	}
}

func TestStaticAssetKeepsNotFoundAndFallbackBehaviour(t *testing.T) {
	handler := newTestHandler(t)
	for _, requestPath := range []string{"/admin/assets/", "/admin/assets/missing-abcdefgh.js", "/admin/favicon.ico"} {
		if response := serveStatic(t, handler, staticRequest{path: requestPath, headers: map[string]string{"Accept-Encoding": "gzip"}}); response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", requestPath, response.Code)
		}
	}
	// A directory name without a slash or extension was never a file and
	// keeps falling back to the SPA, as it did when the handler used fs.Stat.
	if response := serveStatic(t, handler, staticRequest{path: "/admin/assets"}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root">`) {
		t.Fatalf("/admin/assets status=%d", response.Code)
	}
}

func TestStaticAssetBuildsEachRepresentationOnceUnderConcurrency(t *testing.T) {
	handler := newTestHandler(t)
	assetPath, original := largestEmbeddedAsset(t, ".js")
	var wait sync.WaitGroup
	bodies := make([][]byte, 16)
	for i := range bodies {
		wait.Add(1)
		go func() {
			defer wait.Done()
			headers := map[string]string{}
			if i%2 == 0 {
				headers["Accept-Encoding"] = "gzip"
			}
			r := httptest.NewRequest(http.MethodGet, assetPath, nil)
			for name, value := range headers {
				r.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			body := response.Body.Bytes()
			if response.Header().Get("Content-Encoding") == "gzip" {
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err == nil {
					body, err = io.ReadAll(reader)
				}
				if err != nil {
					body = nil
				}
			}
			bodies[i] = body
		}()
	}
	wait.Wait()
	for i, body := range bodies {
		if !bytes.Equal(body, original) {
			t.Fatalf("request %d decoded to %d bytes, want %d", i, len(body), len(original))
		}
	}
}

func TestAcceptsGzipFollowsRFC9110(t *testing.T) {
	for value, want := range map[string]bool{
		"": false, "gzip": true, "GZIP": true, "gzip;q=0": false, "gzip; q=0.001": true,
		"gzip;q=0.000": false, "gzip;q=1.0": true, "gzip;q=1.001": false, "gzip;q=2": false,
		"gzip;q=-1": false, "gzip;q=nan": false, "gzip;level=9": false, "*": true, "*;q=0": false,
		"gzip;q=0, *": false, "*;q=0, gzip": true, "identity": false, "deflate, br": false,
		"gzip, gzip;q=0": false, "gzip;q=0, gzip": false,
	} {
		if got := acceptsGzip([]string{value}); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestETagListMatching(t *testing.T) {
	const etag = `"abc"`
	for _, test := range []struct {
		list        string
		weak, match bool
	}{
		{`"abc"`, false, true}, {`W/"abc"`, false, false}, {`W/"abc"`, true, true},
		{`"x", "abc"`, true, true}, {` , "a,b", "abc"`, false, true}, {`"abcd"`, true, false},
		{`*`, false, true}, {`abc`, true, false}, {`"abc`, true, false}, {``, true, false},
	} {
		if got := etagListMatches(test.list, etag, test.weak); got != test.match {
			t.Errorf("etagListMatches(%q, weak=%v) = %v, want %v", test.list, test.weak, got, test.match)
		}
	}
}

// legacyAssetHandler reproduces the serving path this package used before
// precompression: a full fs.ReadFile copy per request, then the API
// middleware's per-request gzip at the default level. It exists only so the
// benchmark can report before/after numbers in one run.
func legacyAssetHandler(name string) http.Handler {
	dist, _ := fs.Sub(Dist, "dist")
	writers := sync.Pool{New: func() any {
		writer, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return writer
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contents, err := fs.ReadFile(dist, name)
		if err != nil {
			http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		if r.Header.Get("Accept-Encoding") == "" {
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(contents))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
		writer := writers.Get().(*gzip.Writer)
		writer.Reset(w)
		_, _ = writer.Write(contents)
		_ = writer.Close()
		writer.Reset(nil)
		writers.Put(writer)
	})
}

// discardResponse is a ResponseWriter that keeps no body, so the benchmark
// measures the handler rather than the recorder's buffer growth.
type discardResponse struct{ header http.Header }

func (d *discardResponse) Header() http.Header         { return d.header }
func (d *discardResponse) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardResponse) WriteHeader(int)             {}

func BenchmarkLargestAdminAsset(b *testing.B) {
	assetPath, original := largestEmbeddedAsset(b, ".js")
	current := newTestHandler(b)
	legacy := legacyAssetHandler(strings.TrimPrefix(assetPath, "/admin/"))
	b.Logf("asset %s: %d bytes", assetPath, len(original))
	for _, encoding := range []string{"gzip", "identity"} {
		for _, variant := range []struct {
			name    string
			handler http.Handler
		}{{"legacy", legacy}, {"current", current}} {
			b.Run(encoding+"/"+variant.name, func(b *testing.B) {
				request := httptest.NewRequest(http.MethodGet, assetPath, nil)
				if encoding == "gzip" {
					request.Header.Set("Accept-Encoding", "gzip")
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(original)))
				for b.Loop() {
					variant.handler.ServeHTTP(&discardResponse{header: http.Header{}}, request)
				}
			})
		}
	}
}
