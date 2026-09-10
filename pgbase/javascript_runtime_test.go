package pgbase

import (
	"context"
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
