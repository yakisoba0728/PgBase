package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestUserMigrationCreateWritesSelfEmbeddedGoFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "migrations")
	var stdout bytes.Buffer
	if err := runUserMigration(context.Background(), []string{"create", "add_widgets", directory}, emptyEnv, testIO(nil, &stdout, io.Discard)); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(stdout.String())
	if filepath.Dir(path) != directory {
		t.Fatalf("generated path=%q", path)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("//go:embed "+filepath.Base(path))) || !bytes.Contains(source, []byte("RegisterUserMigration")) {
		t.Fatalf("generated migration is not source-bound:\n%s", source)
	}
}

func TestUserMigrationDiffWritesExecutableSourceAndWarnings(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000401")
	before, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	after, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat, Collections: []schema.MigrationSnapshotCollection{{Name: "notes", Fields: []schema.FieldInput{{ID: &fieldID, Name: "title", Type: schema.TypeText}}, Rules: []byte(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	beforePath, afterPath, directory := filepath.Join(root, "before.json"), filepath.Join(root, "after.json"), filepath.Join(root, "migrations")
	if err := os.WriteFile(beforePath, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, after, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runUserMigration(context.Background(), []string{"diff", "add_notes", beforePath, afterPath, directory}, emptyEnv, testIO(nil, &stdout, &stderr)); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("ApplyUserMigrationSnapshot")) || !strings.Contains(stderr.String(), "data-loss:") {
		t.Fatalf("source=%s warnings=%q", source, stderr.String())
	}
}

func TestUserMigrationCommandUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"create"}, {"unknown"}, {"up", "extra"}} {
		if err := runUserMigration(context.Background(), args, emptyEnv, testIO(nil, io.Discard, io.Discard)); err == nil {
			t.Fatalf("runUserMigration(%v) succeeded", args)
		}
	}
}
