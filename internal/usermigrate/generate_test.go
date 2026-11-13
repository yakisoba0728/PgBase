package usermigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestGenerateSnapshotSourceRejectsGoKeywords(t *testing.T) {
	snapshot, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Fields("break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var") {
		t.Run(name, func(t *testing.T) {
			if _, err := GenerateSnapshotSource("20260909130000_test", name, snapshot, snapshot); err == nil {
				t.Fatalf("Go keyword %q accepted as package name", name)
			}
		})
	}
}

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
