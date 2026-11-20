//go:build integration

package pgbase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestManagedBackupStartupAllowsRepairWithPendingS3Publication(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool, err := database.Open(ctx, databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	archive := []byte("pending archive")
	digest := sha256.Sum256(archive)
	metadata, err := json.Marshal(backup.Entry{ID: id, Size: int64(len(archive)), SHA256: hex.EncodeToString(digest[:]), StorageFormat: "pgbase-backup-v1"})
	if err != nil {
		t.Fatal(err)
	}
	var objectsMu sync.Mutex
	objects := map[string][]byte{
		"/backup-bucket/" + id + ".json":   metadata,
		"/backup-bucket/" + id + ".tar.gz": archive,
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=repaired-key/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		objectsMu.Lock()
		defer objectsMu.Unlock()
		switch r.Method {
		case http.MethodGet:
			data, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			objects[r.URL.Path] = data
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer storage.Close()

	directory := t.TempDir()
	secret := []byte("managed-backup-test-token-secret-32")
	settings := backup.StorageConfig{Driver: "s3", Bucket: "backup-bucket", Region: "us-east-1", Endpoint: storage.URL, UsePathStyle: true, AccessKey: "expired-key", SecretKey: "expired-secret"}
	if _, err := backup.NewStorageController(ctx, pool, secret, backup.NewLocalRepository(directory), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.backup_storage_operations(backup_id,settings_revision,status) values($1,1,'pending')`, id); err != nil {
		t.Fatal(err)
	}
	assertReceipt := func(want string, wantRevision int64) {
		t.Helper()
		var revision int64
		var status string
		if err := pool.QueryRow(ctx, `select status,settings_revision from pgbase_meta.backup_storage_operations where backup_id=$1`, id).Scan(&status, &revision); err != nil || status != want || revision != wantRevision {
			t.Fatalf("backup receipt: status=%s revision=%d error=%v, want %s/%d", status, revision, err, want, wantRevision)
		}
	}

	address := unusedAddress(t)
	origin := "http://" + address
	options := Options{DatabaseURL: databaseURL, HTTPAddress: address, PublicURL: mustManagedURL(t, origin), SetupToken: strings.Repeat("s", 32), TokenSecret: secret, AllowInsecureDatabase: true, Backups: BackupOptions{Directory: directory}}
	done := make(chan error, 1)
	go func() { done <- RunManaged(ctx, options) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("managed host did not stop")
		}
	}()
	waitManagedLive(t, origin+"/health/ready", done)
	cookie, csrf := setupManagedAdmin(t, origin, options.SetupToken)
	path := origin + "/api/admin/storage/backups"
	saved := managedJSON[backup.StorageSettings](t, http.MethodGet, path, nil, cookie, "", http.StatusOK)
	managedBytes(t, http.MethodPost, path+"/test", nil, "", cookie, csrf, http.StatusServiceUnavailable)
	assertReceipt("pending", saved.Revision)
	managedJSON[map[string]any](t, http.MethodPut, path, map[string]any{"driver": "local", "revision": saved.Revision}, cookie, csrf, http.StatusConflict)

	update := map[string]any{
		"driver": "s3", "bucket": settings.Bucket, "region": settings.Region, "endpoint": storage.URL,
		"usePathStyle": true, "accessKey": "repaired-key", "secretKey": "repaired-secret", "revision": saved.Revision,
	}
	repaired := managedJSON[backup.StorageSettings](t, http.MethodPut, path, update, cookie, csrf, http.StatusOK)
	managedBytes(t, http.MethodPost, path+"/test", nil, "", cookie, csrf, http.StatusNoContent)
	assertReceipt("committed", repaired.Revision)
}
