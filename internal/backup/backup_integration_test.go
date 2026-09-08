//go:build integration

package backup

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestCreateAndRestoreDatabaseAndLocalFiles(t *testing.T) {
	ctx := context.Background()
	sourceURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, sourceURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create table app.backup_probe(value text not null); insert into app.backup_probe values ('restored')`); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	sourceFiles := t.TempDir()
	if err = os.Mkdir(filepath.Join(sourceFiles, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sourceFiles, "nested", "object.bin"), []byte("local-object"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sourceFiles, "database.dump"), []byte("ordinary-local-file"), 0600); err != nil {
		t.Fatal(err)
	}
	backupDirectory := filepath.Join(t.TempDir(), "snapshot")
	if err = Create(ctx, backupDirectory, sourceURL, sourceFiles); err != nil {
		t.Fatal(err)
	}
	emptyBackupDirectory := filepath.Join(t.TempDir(), "empty-files-snapshot")
	if err = Create(ctx, emptyBackupDirectory, sourceURL, t.TempDir()); err != nil {
		t.Fatalf("create with empty configured file directory: %v", err)
	}
	if manifest, verifyErr := Verify(emptyBackupDirectory); verifyErr != nil || manifest.Files == nil || len(manifest.Files) != 0 {
		t.Fatalf("empty configured file backup = %#v, %v", manifest.Files, verifyErr)
	}
	if _, err = os.Stat(filepath.Join(sourceFiles, "nested", "object.bin")); err != nil {
		t.Fatalf("create consumed source file: %v", err)
	}
	if err = Restore(ctx, backupDirectory, testutil.NewDatabase(t), ""); err == nil {
		t.Fatal("restore silently omitted local files")
	}
	conflictURL := testutil.NewDatabase(t)
	conflictConn, err := pgx.Connect(ctx, conflictURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conflictConn.Exec(ctx, `create schema app`); err != nil {
		t.Fatal(err)
	}
	if err = conflictConn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	failedFiles := filepath.Join(t.TempDir(), "failed-restore-files")
	if err = Restore(ctx, backupDirectory, conflictURL, failedFiles); err == nil {
		t.Fatal("restore unexpectedly succeeded with conflicting schema")
	}
	if _, statErr := os.Stat(failedFiles); !os.IsNotExist(statErr) {
		t.Fatalf("failed restore retained installed files: %v", statErr)
	}
	targetURL := testutil.NewDatabase(t)
	targetFiles := filepath.Join(t.TempDir(), "restored-files")
	if err = Restore(ctx, backupDirectory, targetURL, targetFiles); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var value string
	if err = conn.QueryRow(ctx, `select value from app.backup_probe`).Scan(&value); err != nil || value != "restored" {
		t.Fatalf("restored row = %q, %v", value, err)
	}
	content, err := os.ReadFile(filepath.Join(targetFiles, "nested", "object.bin"))
	if err != nil || string(content) != "local-object" {
		t.Fatalf("restored file = %q, %v", content, err)
	}
	if content, err = os.ReadFile(filepath.Join(targetFiles, "database.dump")); err != nil || string(content) != "ordinary-local-file" {
		t.Fatalf("same-name restored file = %q, %v", content, err)
	}
	if _, err = Verify(backupDirectory); err != nil {
		t.Fatalf("source backup after restore = %v", err)
	}
}
