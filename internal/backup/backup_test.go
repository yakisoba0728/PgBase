package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
