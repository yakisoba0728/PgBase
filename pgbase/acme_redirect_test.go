package pgbase

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestACMERedirectRequiresConfiguredHost(t *testing.T) {
	handler := acmeRedirectHandler([]string{"acme.test"})
	unknown := httptest.NewRequest(http.MethodGet, "http://attacker.test/path?q=1", nil)
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest || unknownResponse.Header().Get("Location") != "" {
		t.Fatalf("unknown host response=%d location=%q", unknownResponse.Code, unknownResponse.Header().Get("Location"))
	}

	allowed := httptest.NewRequest(http.MethodGet, "http://ACME.TEST:80/path?q=1", nil)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusTemporaryRedirect || allowedResponse.Header().Get("Location") != "https://acme.test/path?q=1" {
		t.Fatalf("allowed host response=%d location=%q", allowedResponse.Code, allowedResponse.Header().Get("Location"))
	}
}
