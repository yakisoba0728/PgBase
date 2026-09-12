package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesAdminRootAssetsAndSPAFallback(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	assertResponse := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	redirect := assertResponse("/admin")
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/admin/" {
		t.Fatalf("admin redirect status=%d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}

	for _, path := range []string{"/admin/", "/admin/collections", "/admin/collections/posts/records/record-id"} {
		response := assertResponse(path)
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("SPA %s status=%d content-type=%q body=%q", path, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("SPA %s Cache-Control=%q", path, response.Header().Get("Cache-Control"))
		}
	}

	entries, err := fs.Glob(Dist, "dist/assets/*")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded assets: entries=%v err=%v", entries, err)
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry)
		if extension != ".js" && extension != ".css" {
			continue
		}
		response := assertResponse("/admin/" + strings.TrimPrefix(entry, "dist/"))
		if response.Code != http.StatusOK {
			t.Fatalf("asset %s status=%d", entry, response.Code)
		}
		if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("asset %s Cache-Control=%q", entry, response.Header().Get("Cache-Control"))
		}
		wantType := "text/javascript"
		if extension == ".css" {
			wantType = "text/css"
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), wantType) {
			t.Fatalf("asset %s Content-Type=%q, want prefix %q", entry, response.Header().Get("Content-Type"), wantType)
		}
	}
}

func TestHandlerWithExtensionsInjectsMainAndServesScopedAssets(t *testing.T) {
	handler, err := Handler([]UIExtension{{Name: "reports", FS: fstest.MapFS{
		"main.js":                  {Data: []byte(`window.pgbaseAdmin.registerScreen({id:"reports",label:"Reports",path:"/admin/ext/reports",mount:function(node){node.textContent="report extension"}});`)},
		"assets/chart-a1b2c3d4.js": {Data: []byte("chart")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	index := request("/admin/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `src="/admin/extensions.js"`) {
		t.Fatalf("index status=%d body=%q", index.Code, index.Body.String())
	}
	main := request("/admin/extensions.js")
	if main.Code != http.StatusOK || !strings.Contains(main.Body.String(), "registerScreen") || !strings.Contains(main.Body.String(), "await (async function(){") {
		t.Fatalf("main status=%d type=%q body=%q", main.Code, main.Header().Get("Content-Type"), main.Body.String())
	}
	asset := request("/admin/extensions/reports/assets/chart-a1b2c3d4.js")
	if asset.Code != http.StatusOK || asset.Body.String() != "chart" || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset status=%d cache=%q body=%q", asset.Code, asset.Header().Get("Cache-Control"), asset.Body.String())
	}
	for _, path := range []string{"/admin/extensions/missing/main.js", "/admin/extensions/reports/../../index.html", "/admin/extensions/reports/missing.js"} {
		if response := request(path); response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}

func TestHandlerFallsBackForNamesTheEmbeddedFilesystemRejects(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}

	// fs.Sub rejects these names with fs.ErrInvalid rather than fs.ErrNotExist.
	for _, path := range []string{"/admin/collections/", "/admin/collections/posts/records/", "/admin/%FF"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("SPA %s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	// Asset-shaped names stay 404 so the widened filter does not serve the SPA
	// for a missing bundle.
	for _, path := range []string{"/admin/assets/", "/admin/assets/%FF", "/admin/missing.js"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("asset %s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestHandlerAppliesAdminSecurityHeaders(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	wantCSP := "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	if response.Header().Get("Content-Security-Policy") != wantCSP {
		t.Fatalf("CSP=%q", response.Header().Get("Content-Security-Policy"))
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("security headers=%v", response.Header())
	}
}

func TestHandlerNeverFallsBackForRootAPIUnsafeMethodsOrTraversal(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/"},
		{http.MethodGet, "/api"},
		{http.MethodGet, "/api/"},
		{http.MethodGet, "/api/admin/collections"},
		{http.MethodPost, "/admin/collections"},
		{http.MethodGet, "/admin/../index.html"},
		{http.MethodGet, "/admin/%2e%2e/index.html"},
		{http.MethodGet, "/admin/assets/../../index.html"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code == http.StatusOK || strings.Contains(response.Body.String(), `<div id="root"></div>`) {
				t.Fatalf("status=%d served SPA body=%q", response.Code, response.Body.String())
			}
		})
	}
}
