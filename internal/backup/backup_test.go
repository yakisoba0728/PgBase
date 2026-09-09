package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type cancelAfterChecks struct {
	context.Context
	remaining atomic.Int32
}

func newCancelAfterChecks(count int32) *cancelAfterChecks {
	ctx := &cancelAfterChecks{Context: context.Background()}
	ctx.remaining.Store(count)
	return ctx
}

func (c *cancelAfterChecks) Err() error {
	if c.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return nil
}

func validEmptyBackup(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "database.dump"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":1,"createdAt":"2026-01-01T00:00:00Z","postgresqlMajor":18,"database":{"path":"database.dump","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"files":[]}`
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestSafeTreeRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := safeTree(context.Background(), root); err == nil {
		t.Fatal("safeTree accepted a symlink")
	}
}

func TestSafeTreeEmptyDirectoryReturnsEmptyInventory(t *testing.T) {
	items, err := safeTree(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("empty inventory = %#v", items)
	}
}

func TestCopyRegularHonorsCancelledContext(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, make([]byte, 1024), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyRegular(ctx, source, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyRegular error = %v", err)
	}
}

func TestVerifyContextPropagatesCancellationWhileHashing(t *testing.T) {
	// Checks occur at entry, before the manifest item, and in checksum's reader.
	ctx := newCancelAfterChecks(3)
	if _, err := VerifyContext(ctx, validEmptyBackup(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyContext error = %v", err)
	}
}

func TestVerifyContextPropagatesCancellationWhileWalkingInventory(t *testing.T) {
	// Let the empty database hash complete, then cancel in safeTree's walk.
	ctx := newCancelAfterChecks(4)
	if _, err := VerifyContext(ctx, validEmptyBackup(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyContext error = %v", err)
	}
}

func TestCommandEnvironmentDropsInheritedLibpqOverrides(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c search_path=attacker")
	t.Setenv("PGSERVICE", "unexpected")
	t.Setenv("PGPASSWORD", "inherited-secret")
	command, err := commandEnvironment("postgres://expected:explicit-secret@localhost:5432/expected?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer command.cleanup()
	for _, entry := range command.env {
		if strings.HasPrefix(entry, "PGOPTIONS=") || strings.HasPrefix(entry, "PGSERVICE=") || strings.Contains(entry, "inherited-secret") || strings.Contains(entry, "explicit-secret") {
			t.Fatalf("unsafe inherited or plaintext credential environment: %q", entry)
		}
	}
}

func TestCommandEnvironmentRejectsConnectionIdentityQueryOverrides(t *testing.T) {
	for _, parameter := range []string{"host=localhost", "hostaddr=127.0.0.1", "port=5433", "user=other", "password=other", "dbname=other", "database=other", "service=other", "servicefile=other"} {
		_, err := commandEnvironment("postgres://expected:credential@localhost:5432/expected?sslmode=disable&" + parameter)
		if err == nil || !strings.Contains(err.Error(), "authority and path") {
			t.Fatalf("identity override %q error = %v", parameter, err)
		}
	}
}

func TestCleanRelativeRejectsTraversalAndNonCanonicalPaths(t *testing.T) {
	for _, value := range []string{"", ".", "../secret", "a/../secret", "/absolute", `windows\path`} {
		if _, err := cleanRelative(value); err == nil {
			t.Fatalf("cleanRelative(%q) succeeded", value)
		}
	}
	if value, err := cleanRelative("nested/object.bin"); err != nil || filepath.ToSlash(value) != "nested/object.bin" {
		t.Fatalf("valid path = %q, %v", value, err)
	}
}
