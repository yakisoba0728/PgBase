package usermigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestManifestValidationAndAppliedRevision(t *testing.T) {
	callback := func(context.Context, pgx.Tx) error { return nil }
	one, two := sha256.Sum256([]byte("one")), sha256.Sum256([]byte("two"))
	valid := []Migration{{ID: "20260909120000_one", Revision: one, Up: callback, Down: callback}}
	if err := validateManifest(valid); err != nil {
		t.Fatal(err)
	}
	if err := inspect(valid, map[string]storedEntry{"20260909120000_one": {revision: two[:]}}); err == nil || !strings.Contains(err.Error(), "revision mismatch") {
		t.Fatalf("changed applied source error=%v", err)
	}
	for _, manifest := range [][]Migration{
		{{ID: "bad", Revision: one, Up: callback, Down: callback}},
		{{ID: "20260909120000_one", Revision: one, Up: callback}},
		{{ID: "20260909120000_one", Revision: one, Up: callback, Down: callback}, {ID: "20260909110000_two", Revision: two, Up: callback, Down: callback}},
	} {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("invalid manifest accepted: %#v", manifest)
		}
	}
}

func TestGenerateSnapshotSourceIsDeterministicAndBuilds(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000021")
	before, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	after, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat, Collections: []schema.MigrationSnapshotCollection{{
		Name: "notes", Fields: []schema.FieldInput{{ID: &fieldID, Name: "title", Type: schema.TypeText}}, Rules: []byte(`{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	one, err := GenerateSnapshotSource("20260909130000_add_notes", "migrations", before, after)
	if err != nil {
		t.Fatal(err)
	}
	two, err := GenerateSnapshotSource("20260909130000_add_notes", "migrations", before, after)
	if err != nil || !bytes.Equal(one.Source, two.Source) {
		t.Fatalf("generation not deterministic err=%v", err)
	}
	for _, fragment := range []string{"ApplyUserMigrationSnapshot", "BeforeSnapshot:", "DATA LOSS:"} {
		if !strings.Contains(string(one.Source), fragment) {
			t.Fatalf("generated source missing %q", fragment)
		}
	}
	directory := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, one.Filename), one.Source, 0o644); err != nil {
		t.Fatal(err)
	}
	module := filepath.Dir(directory)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module generated.snapshot.test\n\ngo 1.26.0\n\nrequire github.com/yakisoba0728/PgBase v0.0.0\nreplace github.com/yakisoba0728/PgBase => " + repository + "\n"
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = module
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.8")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated snapshot migration does not build: %v\n%s", err, output)
	}
}

func TestGenerateSnapshotSourceRefusesIrreversibleDown(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000022")
	before, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat, Collections: []schema.MigrationSnapshotCollection{{
		Name: "notes", Fields: []schema.FieldInput{{ID: &fieldID, Name: "title", Type: schema.TypeText}}, Rules: []byte(`{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := schema.MarshalMigrationSnapshot(schema.MigrationSnapshot{Format: schema.MigrationSnapshotFormat})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := GenerateSnapshotSource("20260909130001_drop_notes", "migrations", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Impact.Irreversible) == 0 || !bytes.Contains(generated.Source, []byte("IrreversibleMigrationError")) ||
		!bytes.Contains(generated.Source, []byte("IrreversibleReason:")) {
		t.Fatalf("destructive source did not refuse Down: impact=%+v\n%s", generated.Impact, generated.Source)
	}
}

func TestMaterializeSourceIsIdempotentAndPreservesConflict(t *testing.T) {
	recorder := &AutoRecorder{directory: t.TempDir()}
	if err := recorder.materialize("20260909130000_test.go", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.materialize("20260909130000_test.go", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.materialize("20260909130000_test.go", []byte("second")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("conflict error=%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(recorder.directory, "20260909130000_test.go"))
	if err != nil || string(raw) != "first" {
		t.Fatalf("conflict overwrote source=%q err=%v", raw, err)
	}
}

func TestGenerateSelfEmbedsExactGoSource(t *testing.T) {
	directory := t.TempDir()
	path, err := Generate(directory, "add_widgets", time.Date(2026, 9, 9, 12, 34, 56, 0, time.FixedZone("test", 9*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"20260909033456_add_widgets", "//go:embed 20260909033456_add_widgets.go", "RegisterUserMigration", "func(ctx context.Context, tx pgx.Tx) error"} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("generated source missing %q", fragment)
		}
	}
	if _, err := Generate(directory, "add_widgets", time.Date(2026, 9, 9, 12, 34, 56, 0, time.FixedZone("test", 9*60*60))); err == nil {
		t.Fatal("duplicate migration file overwritten")
	}
}

func TestGenerateSameSecondDifferentNamesBuildTogether(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "migrations")
	now := time.Date(2026, 9, 9, 12, 34, 56, 0, time.UTC)
	for _, name := range []string{"add_widgets", "add_gadgets"} {
		if _, err := Generate(directory, name, now); err != nil {
			t.Fatal(err)
		}
	}
	module := filepath.Dir(directory)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module generated.test\n\ngo 1.26.0\n\nrequire github.com/yakisoba0728/PgBase v0.0.0\nreplace github.com/yakisoba0728/PgBase => " + repository + "\n"
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = module
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.8")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated migrations do not build: %v\n%s", err, output)
	}
}
