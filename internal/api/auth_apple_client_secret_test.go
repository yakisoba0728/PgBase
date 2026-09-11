package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAppleClientSecretRouteRequiresAdminAndReturnsNoStoreSecret(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: time.Now().Add(time.Hour)}
	handler := mustRouter(t, Dependencies{AdminAuth: auth, AuthAdministration: &fakeAuthAdministration{}, PublicURL: mustURL("https://pgbase.example")})
	unauthorized := httptest.NewRequest(http.MethodPost, "/api/admin/settings/apple/generate-client-secret", bytes.NewBufferString(`{}`))
	unauthorized.Header.Set("Origin", "https://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", response.Code, response.Body.String())
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"clientId": "com.example.web", "teamId": "ABCDEFGHIJ", "keyId": "KLMNOPQRST", "privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "duration": 60})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/settings/apple/generate-client-secret", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://pgbase.example")
	request.Header.Set("X-PgBase-CSRF", session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || bytes.Contains(response.Body.Bytes(), []byte("privateKey")) {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var output struct {
		Secret string `json:"secret"`
	}
	if json.Unmarshal(response.Body.Bytes(), &output) != nil || output.Secret == "" {
		t.Fatalf("secret response=%s", response.Body.String())
	}
}
