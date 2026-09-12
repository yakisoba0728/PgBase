package usermigrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteExclusiveDoesNotRemoveAnExistingMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.go")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(path, []byte("replacement")); err == nil {
		t.Fatal("writeExclusive replaced an existing migration")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("existing migration = %q", got)
	}
}
