//go:build integration

package cli

import (
	"encoding/json"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/pgbase"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseCLIUsesManagedRestoreTarget(t *testing.T) {
	targetURL := testutil.NewDatabase(t)
	root := t.TempDir()
	files := t.TempDir()
	raw, err := json.Marshal(pgbase.RestoreTarget{DatabaseURL: targetURL, FilesDirectory: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-target.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	base := migrationEnv("postgres://fixture:fixture@127.0.0.1:1/inactive?sslmode=disable")
	env := func(name string) (string, bool) {
		if name == "PGBASE_BACKUPS_DIRECTORY" {
			return root, true
		}
		return base(name)
	}
	runtime, err := openDatabase(t.Context(), env, testIO(nil, io.Discard, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.options.DatabaseURL != targetURL || runtime.options.Files.LocalDirectory != files || runtime.options.Files.Driver != "local" {
		t.Fatalf("resolved target=%+v", runtime.options.Files)
	}
	var current string
	if err := runtime.pool.QueryRow(t.Context(), "select current_database()").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current == "inactive" {
		t.Fatal("opened inactive database")
	}
}
func TestOfflineBackupDoesNotCountItsValidationConnection(t *testing.T) {
	url := testutil.NewDatabase(t)
	pool, err := database.Open(t.Context(), url, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.New(pool, nil).Up(t.Context()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()
	root := t.TempDir()
	raw, err := json.Marshal(pgbase.RestoreTarget{DatabaseURL: url, FilesDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-target.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	base := migrationEnv("postgres://fixture:fixture@127.0.0.1:1/inactive?sslmode=disable")
	env := func(name string) (string, bool) {
		if name == "PGBASE_BACKUPS_DIRECTORY" {
			return root, true
		}
		return base(name)
	}
	directory := filepath.Join(t.TempDir(), "archive")
	if err := runBackup(t.Context(), []string{"create", "--directory", directory}, env, testIO(nil, io.Discard, io.Discard)); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Verify(directory); err != nil {
		t.Fatal(err)
	}
}
