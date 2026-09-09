package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestUserMigrationCommandUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"create"}, {"unknown"}, {"up", "extra"}} {
		if err := runUserMigration(context.Background(), args, emptyEnv, testIO(nil, io.Discard, io.Discard)); err == nil {
			t.Fatalf("runUserMigration(%v) succeeded", args)
		}
	}
}
