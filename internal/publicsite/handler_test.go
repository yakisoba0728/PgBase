package publicsite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHandlerServesAssetsAndSPAWithoutServingMissingAssets(t *testing.T) {
	root := t.TempDir()
	writeSiteFile(t, root, "index.html", "<html>app</html>")
	writeSiteFile(t, root, "assets/app-01234567.js", "console.log(1)")
	h, err := New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, target string
		status         int
		body           string
		cache          string
	}{
		{http.MethodGet, "/assets/app-01234567.js", http.StatusOK, "console.log(1)", "public, max-age=31536000, immutable"},
		{http.MethodHead, "/assets/app-01234567.js", http.StatusOK, "", "public, max-age=31536000, immutable"},
		{http.MethodGet, "/dashboard/items/1", http.StatusOK, "<html>app</html>", "no-cache"},
		{http.MethodGet, "/assets/missing.js", http.StatusNotFound, "404 page not found\n", ""},
		{http.MethodGet, "/missing.css", http.StatusNotFound, "404 page not found\n", ""},
		{http.MethodPost, "/dashboard", http.StatusMethodNotAllowed, "Method not allowed\n", ""},
	} {
		t.Run(test.method+test.target, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
			if response.Code != test.status || response.Body.String() != test.body || response.Header().Get("Cache-Control") != test.cache {
				t.Fatalf("status=%d body=%q cache=%q", response.Code, response.Body.String(), response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandlerRejectsTraversalAndEscapingSymlinks(t *testing.T) {
	root := t.TempDir()
	writeSiteFile(t, root, "index.html", "app")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/%5Csecret.txt", "/escape.txt"} {
		if target == "/escape.txt" && runtime.GOOS == "windows" {
			continue
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || response.Body.String() == "secret" {
			t.Fatalf("%s status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
}

func TestHandlerRequiresAbsoluteExistingDirectory(t *testing.T) {
	if _, err := New("relative", false); err == nil {
		t.Fatal("relative directory accepted")
	}
	if _, err := New(filepath.Join(t.TempDir(), "missing"), false); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func writeSiteFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
