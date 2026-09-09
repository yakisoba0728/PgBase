package api

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/backup"
	"github.com/webisnoteasy/PgBase/internal/jobs"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

type consoleJobs struct{ calls int }

type consoleBackups struct{ calls []string }

func (b *consoleBackups) List(context.Context) ([]backup.Entry, error) {
	b.calls = append(b.calls, "list")
	return []backup.Entry{{ID: "12345678-1234-4567-8901-123456789abc", Size: 7}}, nil
}
func (b *consoleBackups) Create(context.Context) (backup.Job, error) {
	b.calls = append(b.calls, "create")
	return backup.Job{ID: "job", Status: "running"}, nil
}
func (b *consoleBackups) Upload(_ context.Context, body io.Reader) (backup.Entry, error) {
	raw, _ := io.ReadAll(body)
	b.calls = append(b.calls, "upload:"+string(raw))
	return backup.Entry{ID: "uploaded"}, nil
}
func (b *consoleBackups) Open(context.Context, string) (io.ReadCloser, error) {
	b.calls = append(b.calls, "open")
	return io.NopCloser(strings.NewReader("archive")), nil
}
func (b *consoleBackups) Delete(context.Context, string) error {
	b.calls = append(b.calls, "delete")
	return nil
}
func (b *consoleBackups) Restore(context.Context, string) (backup.Job, error) {
	b.calls = append(b.calls, "restore")
	return backup.Job{ID: "restore", Status: "running"}, nil
}
func (b *consoleBackups) Job(context.Context, string) (backup.Job, error) {
	b.calls = append(b.calls, "job")
	return backup.Job{ID: "job", Status: "completed"}, nil
}

func (j *consoleJobs) ListJobs(context.Context) ([]jobs.Status, error) { return []jobs.Status{}, nil }
func (j *consoleJobs) ConfigureJob(context.Context, uuid.UUID, string, bool) error {
	j.calls++
	return nil
}

func TestBackupRoutesAdmissionStreamingAndReceipts(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: time.Now().Add(time.Hour)}
	backups := &consoleBackups{}
	router := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("https://admin.example.com"), Backups: backups})
	request := func(method, path, body, contentType string, csrf bool) *httptest.ResponseRecorder {
		req := authenticatedRecordRequest(method, path, body, csrf)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if csrf {
			req.Header.Set("Origin", "https://admin.example.com")
			req.Header.Set("X-PgBase-CSRF", session.CSRFToken)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	if got := request("GET", "/api/admin/backups", "", "", false); got.Code != 200 || !strings.Contains(got.Body.String(), `"size":7`) {
		t.Fatalf("list = %d %s", got.Code, got.Body.String())
	}
	if got := request("POST", "/api/admin/backups", "", "", true); got.Code != 202 {
		t.Fatalf("create = %d %s", got.Code, got.Body.String())
	}
	if got := request("POST", "/api/admin/backups/upload", "bad", "application/json", true); got.Code != 415 {
		t.Fatalf("bad upload = %d %s", got.Code, got.Body.String())
	}
	if got := request("POST", "/api/admin/backups/upload", "gzip", "application/gzip", true); got.Code != 201 {
		t.Fatalf("upload = %d %s", got.Code, got.Body.String())
	}
	if got := request("GET", "/api/admin/backups/id", "", "", false); got.Code != 200 || got.Body.String() != "archive" || got.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download = %d %#v %q", got.Code, got.Header(), got.Body.String())
	}
	if got := request("DELETE", "/api/admin/backups/id", "", "", false); got.Code != 403 {
		t.Fatalf("unprotected delete = %d", got.Code)
	}
	if got := request("POST", "/api/admin/backups/id/restore", "", "", true); got.Code != 202 {
		t.Fatalf("restore = %d %s", got.Code, got.Body.String())
	}
	if got := request("GET", "/api/admin/backup-jobs/job", "", "", false); got.Code != 200 || !strings.Contains(got.Body.String(), "completed") {
		t.Fatalf("job = %d %s", got.Code, got.Body.String())
	}
}

func TestBackupStreamingRoutesDeclareTransferDeadline(t *testing.T) {
	routes := backupRoutes(&consoleBackups{})
	for _, route := range routes {
		transfer := route.pattern == "/api/admin/backups/upload" ||
			(route.method == "GET" && route.pattern == "/api/admin/backups/{id}")
		if transfer && route.deadline != deadlineBackupTransfer {
			t.Fatalf("%s deadline = %v, want backup transfer", route, route.deadline)
		}
		if !transfer && route.deadline == deadlineBackupTransfer {
			t.Fatalf("non-transfer route %s has backup transfer deadline", route)
		}
	}
}

func TestContextBackupReaderStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	buffer := make([]byte, 1)
	if _, err := (contextBackupReader{ctx: ctx, reader: strings.NewReader("archive")}).Read(buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read error = %v", err)
	}
}
func (j *consoleJobs) RequestRun(context.Context, uuid.UUID) error { j.calls++; return nil }

func (j *consoleJobs) ConfigureJobInTimezone(_ context.Context, _ uuid.UUID, schedule, timezone string, _ bool) error {
	if schedule != "0 9 * * 1" || timezone != "Asia/Seoul" {
		return jobs.ErrInvalidSchedule
	}
	j.calls++
	return nil
}

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

func TestOperationsConsoleDurableLogAndCalendarEndpoints(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: time.Now().Add(time.Hour)}
	log := &observability.RequestLog{}
	log.Record("GET", "/safe/{id}", 204, 7*time.Millisecond)
	worker := &consoleJobs{}
	router := mustRouter(t, Dependencies{AdminAuth: auth, PublicURL: mustURL("https://admin.example.com"), RequestLog: log, Jobs: worker})
	for _, test := range []struct {
		method, path, body string
		csrf               bool
		want               int
	}{
		{"GET", "/api/admin/logs?method=GET&route=%2Fsafe%2F%7Bid%7D", "", false, 200},
		{"GET", "/api/admin/logs/1", "", false, 200},
		{"GET", "/api/admin/logs/statistics", "", false, 200},
		{"GET", "/api/admin/logs?from=not-a-time", "", false, 400},
		{"PUT", "/api/admin/jobs/12345678-1234-4567-8901-123456789abc", `{"schedule":"0 9 * * 1","timezone":"Asia/Seoul","enabled":true}`, true, 204},
		{"DELETE", "/api/admin/logs", "", false, 403},
		{"DELETE", "/api/admin/logs", "", true, 204},
	} {
		response := httptest.NewRecorder()
		request := authenticatedRecordRequest(test.method, test.path, test.body, test.csrf)
		if test.csrf {
			request.Header.Set("Origin", "https://admin.example.com")
			request.Header.Set("X-PgBase-CSRF", session.CSRFToken)
		}
		router.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if worker.calls != 1 {
		t.Fatalf("calendar configure calls = %d", worker.calls)
	}
}
