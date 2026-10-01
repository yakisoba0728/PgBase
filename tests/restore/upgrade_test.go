//go:build restore && upgrade

package restore

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This populated historical-source drill is deliberately separate from an
// acceptance run against a user's real installation backup. It proves both
// forward migration and recovery by restoring the pre-upgrade recovery set,
// rather than attempting to run an old binary on a newer schema.
func TestHistoricalUpgradeAndRollback(t *testing.T) {
	current := newCandidate(t)
	if current.release {
		t.Skip("historical source compatibility is a local source drill")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	const baseline = "2939f1543e4889c0ffa967eae398c91d0ecbb6ce"
	old := *current
	old.binary = buildHistoricalCandidate(t, ctx, baseline)
	old.fileDirectory = t.TempDir()
	images := postgresMatrix()
	if len(images) == 0 {
		t.Fatal("historical upgrade requires a PostgreSQL image")
	}
	selected := images[0]
	for _, image := range images {
		if image.major == "17" {
			selected = image
		}
	}
	postgres := startPostgres(t, ctx, current, selected)
	expected := seedSourceProject(t, ctx, &old, postgres)
	oldSchema := mustAtoi(t, postgres.query(t, ctx, sourceDatabase, "select max(version) from pgbase_meta.system_migrations"))
	if expected.systemMigration != 40 {
		t.Fatalf("historical fixture migration count=%d, want 40", expected.systemMigration)
	}
	sentinel := insertReconciliationSentinel(t, ctx, postgres)
	dumpAndRestore(t, ctx, postgres)
	copyFiles := func() string {
		destination := filepath.Join(t.TempDir(), "files")
		if err := os.CopyFS(destination, os.DirFS(old.fileDirectory)); err != nil {
			t.Fatal(err)
		}
		return destination
	}
	current.fileDirectory = copyFiles()
	if output, err := current.command(ctx, current.databaseURL(postgres, restoredDatabase), "", "migrate", "up"); err != nil {
		t.Fatalf("upgrade historical database: %v\n%s", err, output)
	}
	upgraded := expected
	upgraded.systemMigration = mustAtoi(t, postgres.query(t, ctx, restoredDatabase, "select count(*) from pgbase_meta.system_migrations"))
	if upgraded.systemMigration <= expected.systemMigration {
		t.Fatal("the historical fixture did not exercise a schema upgrade")
	}
	newSchema := mustAtoi(t, postgres.query(t, ctx, restoredDatabase, "select max(version) from pgbase_meta.system_migrations"))
	t.Logf("historical %s schema version %d -> %d (%d -> %d migration entries)", baseline, oldSchema, newSchema, expected.systemMigration, upgraded.systemMigration)
	verifyRestoredDatabase(t, ctx, postgres, upgraded)
	verifyRestoredDoctor(t, ctx, current, postgres)
	if !t.Run("upgraded-service", func(t *testing.T) {
		verifyRestoredService(t, ctx, current, postgres, upgraded, sentinel)
	}) {
		t.Fatal("upgraded service acceptance failed")
	}
	// The child cleanup has waited for the exact upgraded process to exit.
	// Recreate only this drill's disposable target database, retaining source.
	if output, err := postgres.exec(ctx, "dropdb", "--username", databaseUser, restoredDatabase); err != nil {
		t.Fatalf("discard disposable upgraded target: %v\n%s", err, output)
	}
	if output, err := postgres.exec(ctx, "createdb", "--username", databaseUser, restoredDatabase); err != nil {
		t.Fatalf("create rollback target: %v\n%s", err, output)
	}
	if output, err := postgres.exec(ctx, "pg_restore", "--username="+databaseUser, "--exit-on-error", "--no-owner", "--no-privileges", "--single-transaction", "--dbname="+restoredDatabase, "/tmp/pgbase-restore.dump"); err != nil {
		t.Fatalf("restore pre-upgrade database: %v\n%s", err, output)
	}
	rollback := old
	rollback.fileDirectory = copyFiles()
	verifyRestoredDatabase(t, ctx, postgres, expected)
	verifyRestoredDoctor(t, ctx, &rollback, postgres)
	t.Run("old-version-rollback", func(t *testing.T) {
		verifyRestoredService(t, ctx, &rollback, postgres, expected, sentinel)
	})
}

func buildHistoricalCandidate(t *testing.T, ctx context.Context, revision string) string {
	t.Helper()
	archive := exec.CommandContext(ctx, "git", "archive", "--format=tar", revision)
	archive.Dir = repositoryRoot(t)
	contents, err := archive.Output()
	if err != nil {
		t.Fatalf("read historical source %s: %v", revision, err)
	}
	directory := t.TempDir()
	reader := tar.NewReader(bytes.NewReader(contents))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err == nil && header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if err != nil || !filepath.IsLocal(filepath.FromSlash(header.Name)) {
			t.Fatalf("invalid historical archive entry: %v", err)
		}
		path := filepath.Join(directory, filepath.FromSlash(header.Name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unsupported historical archive entry type %d", header.Typeflag)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0777)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("extract historical source: copy=%v close=%v", copyErr, closeErr)
		}
	}
	name := "pgbase"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", binary, "./cmd/pgbase")
	build.Dir = directory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build historical source: %v\n%s", err, output)
	}
	return binary
}
