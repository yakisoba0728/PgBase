package jsvm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/jsvm"
)

func TestLoadBundleSnapshotsConfinedCommonJSGraph(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{
  "format":"pgbase.scripts/v1",
  "routes":[{"method":"POST","pattern":"/tasks","access":"user","handler":{"module":"routes/tasks.js","export":"create"}}]
}`)
	writeFile(t, directory, "routes/tasks.js", `const helper = require("../lib/helper"); exports.create = input => helper.reply(input);`)
	writeFile(t, directory, "lib/helper.js", `exports.reply = input => ({status: 201, json: {name: input.name}});`)

	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if got := bundle.Manifest().Routes[0].Handler; got.Module != "routes/tasks.js" || got.Export != "create" {
		t.Fatalf("route handler = %#v", got)
	}
	if bundle.Digest() == "" {
		t.Fatal("bundle digest is empty")
	}

	writeFile(t, directory, "lib/helper.js", `exports.reply = () => ({status: 204});`)
	if got := bundle.Source("lib/helper.js"); string(got) != `exports.reply = input => ({status: 201, json: {name: input.name}});` {
		t.Fatalf("bundle did not preserve immutable source snapshot: %q", got)
	}
}

func writeFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
