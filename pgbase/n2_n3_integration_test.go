//go:build integration

package pgbase

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/api"
)

func TestN2PublicStaticRoutesKeepPgBaseAndCustomRoutesAuthoritative(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>public app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app-01234567.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.PublicDir, options.PublicSPAFallback = root, true
		options.CustomRoutes = []CustomRoute{{Method: http.MethodGet, Pattern: "/site-check", Access: api.CustomRoutePublic, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("custom")) })}}
	})
	for _, test := range []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodGet, "/assets/app-01234567.js", 200, "console.log(1)"},
		{http.MethodGet, "/dashboard/one", 200, "<html>public app</html>"},
		{http.MethodGet, "/assets/missing.js", 404, "404 page not found\n"},
		{http.MethodGet, "/api/not-a-route", 404, ""},
		{http.MethodPost, "/site-check", 405, "Method not allowed\n"},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.app.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if test.body != "" && response.Body.String() != test.body {
				t.Fatalf("body=%q", response.Body.String())
			}
			if test.status == http.StatusOK && strings.HasPrefix(test.path, "/assets/") {
				policy := response.Header().Get("Content-Security-Policy")
				if !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, "connect-src 'self'") {
					t.Fatalf("public asset CSP=%q", policy)
				}
			}
			if test.path == "/api/not-a-route" && !bytes.Contains(response.Body.Bytes(), []byte(`"code":"not_found"`)) {
				t.Fatalf("API 404 body=%q", response.Body.String())
			}
		})
	}
}

// Without PublicDir the router must not see a typed-nil *publicsite.Handler as
// a configured static site: unknown non-API paths get the ordinary router 404
// instead of a recovered nil-receiver panic (HTTP 500).
func TestN2WithoutPublicDirUnknownPathsUseRouterNotFound(t *testing.T) {
	app := newPublicUsersApp(t, false)
	if app.app.options.PublicDir != "" {
		t.Fatalf("fixture PublicDir=%q, want empty", app.app.options.PublicDir)
	}
	// serve returns the status, content type and body with the per-request ID
	// removed so router 404 bodies from different requests compare equal.
	serve := func(path string) (int, string, string) {
		response := httptest.NewRecorder()
		app.app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == nil {
			return response.Code, response.Header().Get("Content-Type"), response.Body.String()
		}
		delete(body["error"], "requestId")
		normalized, _ := json.Marshal(body)
		return response.Code, response.Header().Get("Content-Type"), string(normalized)
	}
	apiStatus, apiType, apiBody := serve("/api/not-a-route")
	if apiStatus != http.StatusNotFound || !strings.Contains(apiBody, `"code":"not_found"`) {
		t.Fatalf("API 404 status=%d body=%q", apiStatus, apiBody)
	}
	for _, path := range []string{"/foo", "/healthz", "/livez", "/readyz", "/zz", "/dashboard/one"} {
		t.Run(path, func(t *testing.T) {
			status, contentType, body := serve(path)
			if status != apiStatus || contentType != apiType || body != apiBody {
				t.Fatalf("status=%d type=%q body=%q, want router 404 %d %q %q", status, contentType, body, apiStatus, apiType, apiBody)
			}
		})
	}
	if status, _, body := serve("/health/ready"); status != http.StatusOK {
		t.Fatalf("/health/ready status=%d body=%q", status, body)
	}
}

func TestN3AdministratorOTPAndIPPoliciesEnforceHTTPLoginAndSessions(t *testing.T) {
	app := newPublicUsersApp(t, false)
	_, _ = setupPublicUsersAdministrator(t, app)
	service := adminauth.NewService(app.app.pool, adminauth.NewArgon2idPasswords(), []byte("public-users-token-secret-32-bytes-minimum"), publicSetupToken, 12*time.Hour, nil)
	if err := service.EnableTOTP(t.Context(), publicAdminEmail, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	missing := adminLoginRequest(t, app, "")
	if missing.Code != http.StatusUnauthorized || !bytes.Contains(missing.Body.Bytes(), []byte(`"code":"otp_required"`)) {
		t.Fatalf("OTP-free login status=%d body=%q", missing.Code, missing.Body.String())
	}
	issued := adminLoginRequest(t, app, currentTOTP("JBSWY3DPEHPK3PXP", time.Now()))
	if issued.Code != http.StatusOK {
		t.Fatalf("OTP login status=%d body=%q", issued.Code, issued.Body.String())
	}
	cookies := issued.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("OTP login issued no session cookie")
	}
	if err := service.AllowIP(t.Context(), publicAdminEmail, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	allowed.RemoteAddr = "192.0.2.44:4000"
	allowed.AddCookie(cookies[0])
	allowedResponse := httptest.NewRecorder()
	app.app.Handler().ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf("allowed session status=%d body=%q", allowedResponse.Code, allowedResponse.Body.String())
	}
	blocked := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	blocked.RemoteAddr = "198.51.100.44:4000"
	blocked.AddCookie(cookies[0])
	blockedResponse := httptest.NewRecorder()
	app.app.Handler().ServeHTTP(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("blocked session status=%d body=%q", blockedResponse.Code, blockedResponse.Body.String())
	}
}

func adminLoginRequest(t *testing.T, app *publicUsersFixture, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": publicAdminEmail, "password": publicAdminPass, "otpCode": code})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", publicUsersOrigin)
	response := httptest.NewRecorder()
	app.app.Handler().ServeHTTP(response, request)
	return response
}

func currentTOTP(secret string, now time.Time) string {
	key := []byte("Hello!\xde\xad\xbe\xef")
	_ = secret
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := (int(digest[offset])&127)<<24 | int(digest[offset+1])<<16 | int(digest[offset+2])<<8 | int(digest[offset+3])
	return strconv.FormatInt(int64(value%1000000+1000000), 10)[1:]
}
