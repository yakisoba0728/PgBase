package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaScriptCheckAndTypesDoNotRequireDatabaseConfiguration(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/hello","access":"public","handler":{"module":"app.js","export":"hello"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte(`exports.hello=()=>({status:200,json:{ok:true}});`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"js", "check", "--dir", directory}, nil, testIO(nil, &stdout, &stderr)); code != 0 {
		t.Fatalf("js check code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "routes=1 hooks=0 jobs=0 migrations=0") {
		t.Fatalf("js check output = %q", stdout.String())
	}
	stdout.Reset()
	if code := Execute(context.Background(), []string{"js", "types"}, nil, testIO(nil, &stdout, &stderr)); code != 0 {
		t.Fatalf("js types code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `declare module "pgbase"`) || !strings.Contains(stdout.String(), "applySnapshot") {
		t.Fatal("js types omitted runtime bindings")
	}
}

func TestJavaScriptCheckRejectsInvalidBundleAsConfiguration(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"js", "check", "--dir", directory}, nil, testIO(nil, &bytes.Buffer{}, &stderr)); code != 2 {
		t.Fatalf("invalid js check code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "JavaScript bundle is invalid") {
		t.Fatalf("invalid js check stderr = %q", stderr.String())
	}
}

func TestJavaScriptMigrationPreflightsBundleBeforeDatabase(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100040_bad","module":"missing.js","up":"up","irreversibleReason":"fixture"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"migration", "list", "--js-dir", directory},
		migrationEnv("postgres://postgres@127.0.0.1:1/unreachable?sslmode=disable"), testIO(nil, &bytes.Buffer{}, &stderr))
	if code != 2 || !strings.Contains(stderr.String(), "JavaScript bundle is invalid") {
		t.Fatalf("migration preflight code=%d stderr=%q", code, stderr.String())
	}
}
