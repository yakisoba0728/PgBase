package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

func TestWriteFileErrorReportsBusyImageProcessingAsRetryable(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/files/x", nil)
	writeFileError(recorder, request, fmt.Errorf("render variant: %w", filestore.ErrImageBusy))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}
	if !strings.Contains(recorder.Body.String(), "file_busy") {
		t.Fatalf("body = %s, want file_busy code", recorder.Body.String())
	}
}
