//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestManagedBackupHTTPRestoresAndActivatesFreshTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	databaseURL := testutil.NewDatabase(t)
	directory := t.TempDir()
	address := unusedAddress(t)
	origin := "http://" + address
	var logs bytes.Buffer
	options := Options{DatabaseURL: databaseURL, HTTPAddress: address, PublicURL: mustManagedURL(t, origin), SetupToken: strings.Repeat("s", 32), TokenSecret: []byte("managed-backup-test-token-secret-32"), AutoMigrate: true, AllowInsecureDatabase: true, Backups: BackupOptions{Directory: directory}, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	done := make(chan error, 1)
	go func() { done <- RunManaged(ctx, options) }()
	waitManagedLive(t, origin+"/health/live", done)
	cookie, csrf := setupManagedAdmin(t, origin, options.SetupToken)
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(context.Background(), `CREATE TABLE app.managed_restore_marker(value text NOT NULL); INSERT INTO app.managed_restore_marker VALUES('snapshot')`); err != nil {
		t.Fatal(err)
	}
	if err = conn.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	created := managedJSON[backup.Job](t, http.MethodPost, origin+"/api/admin/backups", nil, cookie, csrf, http.StatusAccepted)
	created = waitManagedJob(t, origin, created.ID, cookie)
	if created.Status != "completed" {
		t.Fatalf("create job = %#v", created)
	}
	archive := managedBytes(t, http.MethodGet, origin+"/api/admin/backups/"+created.BackupID, nil, "", cookie, "", http.StatusOK)
	if len(archive) == 0 {
		t.Fatal("downloaded backup is empty")
	}
	uploaded := managedJSON[backup.Entry](t, http.MethodPost, origin+"/api/admin/backups/upload", archive, cookie, csrf, http.StatusCreated, "application/gzip")
	if uploaded.ID == created.BackupID {
		t.Fatal("upload reused backup identity")
	}

	conn, err = pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(context.Background(), `UPDATE app.managed_restore_marker SET value='mutated'`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())
	restored := managedJSON[backup.Job](t, http.MethodPost, origin+"/api/admin/backups/"+created.BackupID+"/restore", nil, cookie, csrf, http.StatusAccepted)
	restored = waitManagedJob(t, origin, restored.ID, cookie)
	if restored.Status != "completed" {
		t.Fatalf("restore job = %#v logs=%s", restored, logs.String())
	}
	raw, err := os.ReadFile(filepath.Join(directory, "active-target.json"))
	if err != nil {
		t.Fatal(err)
	}
	var target RestoreTarget
	if err = json.Unmarshal(raw, &target); err != nil {
		t.Fatal(err)
	}
	targetConn, err := pgx.Connect(context.Background(), target.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err = targetConn.QueryRow(context.Background(), `SELECT value FROM app.managed_restore_marker`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	_ = targetConn.Close(context.Background())
	if value != "snapshot" {
		t.Fatalf("activated marker = %q", value)
	}
	// Make the original target observably unable to authenticate the session
	// captured by the backup. A restart that ignored active-target.json would
	// therefore reject the same cookie.
	original, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = original.Exec(context.Background(), `DELETE FROM pgbase_meta.admin_sessions`); err != nil {
		t.Fatal(err)
	}
	if err = original.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case err = <-done:
		if err != nil && err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("managed host did not stop")
	}

	restartCtx, restartCancel := context.WithCancel(context.Background())
	restarted := make(chan error, 1)
	go func() { restarted <- RunManaged(restartCtx, options) }()
	waitManagedLive(t, origin+"/health/live", restarted)
	managedBytes(t, http.MethodGet, origin+"/api/admin/session", nil, "", cookie, "", http.StatusOK)
	restartCancel()
	select {
	case err = <-restarted:
		if err != nil && err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("restarted managed host did not stop")
	}

	parsedTarget, err := url.Parse(target.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	_, dropErr := cleanup.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{strings.TrimPrefix(parsedTarget.Path, "/")}.Sanitize()+" WITH (FORCE)")
	closeErr := cleanup.Close(context.Background())
	if dropErr != nil || closeErr != nil {
		t.Fatalf("drop restored target: %v / %v", dropErr, closeErr)
	}
}

func mustManagedURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func setupManagedAdmin(t *testing.T, origin, token string) (*http.Cookie, string) {
	return setupManagedAdminWithClient(t, http.DefaultClient, origin, token)
}

func setupManagedAdminWithClient(t *testing.T, client *http.Client, origin, token string) (*http.Cookie, string) {
	response := managedJSONWithClient[struct {
		CSRFToken string `json:"csrfToken"`
	}](t, client, http.MethodPost, origin+"/api/admin/setup", map[string]any{"setupToken": token, "email": "managed@example.com", "password": "managed-backup-password"}, nil, "", http.StatusCreated)
	request, _ := http.NewRequest(http.MethodPost, origin+"/api/admin/login", bytes.NewReader([]byte(`{"email":"managed@example.com","password":"managed-backup-password"}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	var login struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.NewDecoder(result.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookies := result.Cookies()
	if len(cookies) == 0 || response.CSRFToken == "" || login.CSRFToken == "" {
		t.Fatal("admin credentials absent")
	}
	return cookies[0], login.CSRFToken
}

func managedJSON[T any](t *testing.T, method, path string, body any, cookie *http.Cookie, csrf string, want int, contentTypes ...string) T {
	return managedJSONWithClient[T](t, http.DefaultClient, method, path, body, cookie, csrf, want, contentTypes...)
}

func managedJSONWithClient[T any](t *testing.T, client *http.Client, method, path string, body any, cookie *http.Cookie, csrf string, want int, contentTypes ...string) T {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		if bytesBody, ok := body.([]byte); ok {
			raw = bytesBody
		} else {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	response := managedBytesWithClient(t, client, method, path, raw, firstManaged(contentTypes), cookie, csrf, want)
	var value T
	if len(response) > 0 {
		if err = json.Unmarshal(response, &value); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, response)
		}
	}
	return value
}
func managedBytes(t *testing.T, method, path string, body []byte, contentType string, cookie *http.Cookie, csrf string, want int) []byte {
	return managedBytesWithClient(t, http.DefaultClient, method, path, body, contentType, cookie, csrf, want)
}

func managedBytesWithClient(t *testing.T, client *http.Client, method, path string, body []byte, contentType string, cookie *http.Cookie, csrf string, want int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType == "" && len(body) > 0 {
		contentType = "application/json"
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", mustManagedOrigin(t, path))
	}
	if csrf != "" {
		request.Header.Set("X-PgBase-CSRF", csrf)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	raw, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != want {
		t.Fatalf("%s %s = %d: %s", method, path, result.StatusCode, raw)
	}
	return raw
}
func mustManagedOrigin(t *testing.T, path string) string {
	t.Helper()
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Scheme + "://" + parsed.Host
}
func firstManaged(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
func waitManagedJob(t *testing.T, origin, id string, cookie *http.Cookie) backup.Job {
	return waitManagedJobWithClient(t, http.DefaultClient, origin, id, cookie)
}

func waitManagedJobWithClient(t *testing.T, client *http.Client, origin, id string, cookie *http.Cookie) backup.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		request, err := http.NewRequest(http.MethodGet, origin+"/api/admin/backup-jobs/"+id, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(cookie)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		// The managed host deliberately pauses requests while switching targets.
		// Treat only that advertised transition as pending, not as completion.
		if response.StatusCode == http.StatusServiceUnavailable && strings.TrimSpace(string(raw)) == "Restore activation in progress. Retry shortly." {
			// Keep polling the same durable job after activation releases admission.
		} else {
			if response.StatusCode != http.StatusOK {
				t.Fatalf("backup job status = %d: %s", response.StatusCode, raw)
			}
			var job backup.Job
			if err := json.Unmarshal(raw, &job); err != nil {
				t.Fatal(err)
			}
			if job.Status != "running" {
				return job
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup job %s timed out", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitManagedLive(t *testing.T, endpoint string, done <-chan error) {
	waitManagedLiveWithClient(t, http.DefaultClient, endpoint, done)
}

func waitManagedLiveWithClient(t *testing.T, client *http.Client, endpoint string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("managed host stopped before live: %v", err)
		default:
		}
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("managed host did not become live")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
