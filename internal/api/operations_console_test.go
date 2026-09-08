package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jobs"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

type consoleJobs struct{ calls int }

func (j *consoleJobs) ListJobs(context.Context) ([]jobs.Status, error) { return []jobs.Status{}, nil }
func (j *consoleJobs) ConfigureJob(context.Context, uuid.UUID, string, bool) error {
	j.calls++
	return nil
}
func (j *consoleJobs) RequestRun(context.Context, uuid.UUID) error { j.calls++; return nil }

func TestOperationsConsoleAdmissionAndLogRedaction(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: time.Now().Add(time.Hour)}
	log := &observability.RequestLog{}
	worker := &consoleJobs{}
	router := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("https://admin.example.com"), RequestLog: log, Jobs: worker})
	for _, test := range []struct {
		method, path, body string
		csrf               bool
		origin             string
		want               int
	}{
		{"GET", "/api/admin/logs", "", false, "https://foreign.example", 403},
		{"POST", "/api/admin/jobs/12345678-1234-4567-8901-123456789abc/run", "", false, "https://admin.example.com", 403},
		{"POST", "/api/admin/jobs/12345678-1234-4567-8901-123456789abc/run", "", true, "https://admin.example.com", 204},
		{"GET", "/api/admin/logs?before=bad", "", false, "", 400},
		{"GET", "/api/admin/logs?statusClass=9", "", false, "", 400},
	} {
		req := authenticatedRecordRequest(test.method, test.path, test.body, test.csrf)
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.want {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if worker.calls != 1 {
		t.Fatalf("unauthorized mutation: %d calls", worker.calls)
	}
	req := authenticatedRecordRequest("GET", "/api/v1/collections/private-name/records/secret-id?token=secret-token", "", false)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedRecordRequest("GET", "/api/admin/logs", "", false))
	for _, secret := range []string{"private-name", "secret-id", "secret-token", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("log disclosed %s", secret)
		}
	}
	if !strings.Contains(response.Body.String(), "/api/v1/collections/{collection}/records/{id}") {
		t.Fatalf("route template absent: %s", response.Body.String())
	}
}
