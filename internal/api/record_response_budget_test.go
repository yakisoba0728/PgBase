package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordReadRejectsOversizedPublicResponseBeforeSettingETag(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	// JSON escapes each character to six bytes. The raw value easily fits,
	// but the final representation with its framing exceeds the route limit.
	writeRecordReadJSON(response, request, map[string]any{
		"body": strings.Repeat("<", maximumRecordPageResponseBytes/6),
	}, `"record-tag"`)
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "response_too_large") || response.Header().Get("ETag") != "" {
		t.Fatalf("oversized public response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestRecordReadFallsBackIfOptionalDataExceedsWireBudget(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	writeRecordReadJSONWithFallback(response, request, map[string]any{
		"fileMetadata": strings.Repeat("<", maximumRecordPageResponseBytes/6),
	}, "", map[string]any{"items": []string{"record"}})
	if response.Code != http.StatusOK || response.Body.String() != "{\"items\":[\"record\"]}\n" {
		t.Fatalf("fallback response: status=%d body=%s", response.Code, response.Body.String())
	}
}
