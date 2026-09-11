package pgbase

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaScriptRouteUsesBoundedHTTPAdapter(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "routes":[{"method":"POST","pattern":"/extensions/echo/{id}","access":"public","handler":{"module":"route.js","export":"echo"},"maximumRequestBytes":1024,"maximumResponseBytes":2048}]
}`, `exports.echo = request => ({
 status: 201,
 headers: {"X-Extension":"yes"},
 json: {method:request.method,id:request.pathValues.id,body:request.json}
});`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	routes := runtime.routes()
	if len(routes) != 1 {
		t.Fatalf("routes = %d", len(routes))
	}
	request := httptest.NewRequest(http.MethodPost, "/extensions/echo/task-1", strings.NewReader(`{"amount":9007199254740993}`))
	request.SetPathValue("id", "task-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	routes[0].Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("X-Extension") != "yes" {
		t.Fatalf("response code=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"body":{"amount":9007199254740993},"id":"task-1","method":"POST"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestJavaScriptRoutePreservesPublicApiErrors(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/typed-error","access":"public","handler":{"module":"route.js","export":"run"}}]}`, `exports.run=()=>{throw new ApiError(456,"custom error",{field:"invalid"})}`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	response := httptest.NewRecorder()
	runtime.routes()[0].Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/typed-error", nil))
	if response.Code != 456 || response.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(response.Body.String()) != `{"status":456,"message":"custom error","data":{"field":"invalid"}}` {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestJavaScriptBodyLimitBoundsTheHTTPReader(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/limited","access":"public","handler":{"module":"route.js","export":"run"},"maximumRequestBytes":1024}]}`, `exports.run=$apis.bodyLimit(4)((request)=>({status:200,body:request.body}));`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	ok := httptest.NewRecorder()
	runtime.routes()[0].Handler.ServeHTTP(ok, httptest.NewRequest(http.MethodPost, "/limited", strings.NewReader("four")))
	if ok.Code != http.StatusOK || ok.Body.String() != "four" {
		t.Fatalf("accepted response=%d body=%q", ok.Code, ok.Body.String())
	}
	over := httptest.NewRecorder()
	overRequest := httptest.NewRequest(http.MethodPost, "/limited", strings.NewReader("five"))
	overRequest.ContentLength = 5
	runtime.routes()[0].Handler.ServeHTTP(over, overRequest)
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("limited response=%d body=%q", over.Code, over.Body.String())
	}
}

func TestJavaScriptStaticTransportServesRootedFilesWithRangeAndHead(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "root")
	if err := os.MkdirAll(filepath.Join(root, "site"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "site", "asset.txt"), []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "routes":[{"method":"GET","pattern":"/assets/{path...}","access":"public","handler":{"module":"route.js","export":"asset"},"capabilities":["filesystem"],"streamResponse":true}]
}`, `exports.asset=$apis.static("site");`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"filesystem": true}, PocketBaseCompatibility: PocketBaseCompatibilityOptions{FilesystemRoot: root}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	handler := runtime.routes()[0].Handler

	request := httptest.NewRequest(http.MethodGet, "/assets/asset.txt", nil)
	request.SetPathValue("path", "asset.txt")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "abcdef" || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Header().Get("Last-Modified") == "" {
		t.Fatalf("static response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	cachedRequest := httptest.NewRequest(http.MethodGet, "/assets/asset.txt", nil)
	cachedRequest.SetPathValue("path", "asset.txt")
	cachedRequest.Header.Set("If-Modified-Since", response.Header().Get("Last-Modified"))
	cachedResponse := httptest.NewRecorder()
	handler.ServeHTTP(cachedResponse, cachedRequest)
	if cachedResponse.Code != http.StatusNotModified || cachedResponse.Body.Len() != 0 {
		t.Fatalf("cached response=%d headers=%v body=%q", cachedResponse.Code, cachedResponse.Header(), cachedResponse.Body.String())
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/assets/asset.txt", nil)
	rangeRequest.SetPathValue("path", "asset.txt")
	rangeRequest.Header.Set("Range", "bytes=2-4")
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "cde" || rangeResponse.Header().Get("Content-Range") != "bytes 2-4/6" {
		t.Fatalf("range response=%d headers=%v body=%q", rangeResponse.Code, rangeResponse.Header(), rangeResponse.Body.String())
	}

	headRequest := httptest.NewRequest(http.MethodHead, "/assets/asset.txt", nil)
	headRequest.SetPathValue("path", "asset.txt")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, headRequest)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Length") != "6" {
		t.Fatalf("head response=%d headers=%v body=%q", headResponse.Code, headResponse.Header(), headResponse.Body.String())
	}

	traversal := httptest.NewRequest(http.MethodGet, "/assets/secret.txt", nil)
	traversal.SetPathValue("path", "../secret.txt")
	traversalResponse := httptest.NewRecorder()
	handler.ServeHTTP(traversalResponse, traversal)
	if traversalResponse.Code != http.StatusNotFound {
		t.Fatalf("traversal status=%d body=%s", traversalResponse.Code, traversalResponse.Body.String())
	}
}

func TestJavaScriptGzipTransportNegotiatesAndClosesStream(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/compressed","access":"public","handler":{"module":"route.js","export":"compressed"}}]}`, `exports.compressed=$apis.gzip(()=>({status:200,body:"compressed response"}));`)
	runtime, err := prepareJavaScript(JavaScriptOptions{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	handler := runtime.routes()[0].Handler

	request := httptest.NewRequest(http.MethodGet, "/compressed", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip response=%d headers=%v", response.Code, response.Header())
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	if string(body) != "compressed response" {
		t.Fatalf("gzip body=%q", body)
	}

	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/compressed", nil))
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != "compressed response" {
		t.Fatalf("plain headers=%v body=%q", plain.Header(), plain.Body.String())
	}

	encoded := httptest.NewRecorder()
	encoded.Header().Set("Content-Encoding", "identity")
	serveJavaScriptGzip(encoded, request, javaScriptRouteResponse{Status: http.StatusOK, Body: stringPointer("one layer")})
	if encoded.Header().Get("Content-Encoding") != "identity" || encoded.Body.String() != "one layer" {
		t.Fatalf("already encoded headers=%v body=%q", encoded.Header(), encoded.Body.String())
	}
}

func stringPointer(value string) *string { return &value }

func TestInstallJavaScriptRoutesRejectsGoConflict(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "routes":[{"method":"GET","pattern":"/reports/{id}","access":"public","handler":{"module":"route.js","export":"handle"}}]
}`, `module.exports.handle = function () { return {status: 204}; };`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	appOptions := Options{CustomRoutes: []CustomRoute{{Method: http.MethodGet, Pattern: "/reports/{name}", Access: RoutePublic, Handler: http.NotFoundHandler()}}}
	if err := installJavaScriptRoutes(&appOptions, runtime); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("installJavaScriptRoutes conflict = %v", err)
	}
}

func writeJavaScriptFixture(t *testing.T, directory, manifest, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}
