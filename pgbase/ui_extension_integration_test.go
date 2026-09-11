//go:build integration

package pgbase

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestNewServesOperatorRegisteredUIExtension(t *testing.T) {
	app, err := New(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t), TokenSecret: make([]byte, 32), AutoMigrate: true,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		UIExtensions: []UIExtension{{Name: "reports", FS: fstest.MapFS{
			"main.js":    {Data: []byte(`window.pgbaseAdmin.registerScreen({id:"reports",label:"Reports",path:"/admin/ext/reports",mount:function(){}});`)},
			"report.txt": {Data: []byte("operator asset")},
		}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	request := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	if response := request("/admin/"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/admin/extensions.js") {
		t.Fatalf("index status=%d body=%q", response.Code, response.Body.String())
	}
	if response := request("/admin/extensions.js"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "registerScreen") {
		t.Fatalf("main status=%d body=%q", response.Code, response.Body.String())
	}
	if response := request("/admin/extensions/reports/report.txt"); response.Code != http.StatusOK || response.Body.String() != "operator asset" {
		t.Fatalf("asset status=%d body=%q", response.Code, response.Body.String())
	}
}
