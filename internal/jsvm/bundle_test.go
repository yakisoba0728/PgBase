package jsvm_test

import (
	"bytes"
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

func TestMigrationBundleFreezesOnlyDeclaredSources(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100001_create_widget","module":"migration.js","up":"up","down":"down","dependencies":["shared.js"]}]}`
	for name, source := range map[string]string{
		"pgbase.scripts.json": manifest,
		"migration.js":        `const shared=require("./shared"); exports.up=()=>shared.value; exports.down=()=>({});`,
		"shared.js":           `exports.value={ok:true};`,
		"unrelated.js":        `exports.value="ignored";`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	subset, source1, err := bundle.MigrationBundle("202609100001_create_widget")
	if err != nil {
		t.Fatal(err)
	}
	if subset.Source("unrelated.js") != nil || subset.Source("shared.js") == nil {
		t.Fatal("migration source confinement does not match declared dependencies")
	}
	if err := os.WriteFile(filepath.Join(directory, "unrelated.js"), []byte(`exports.value="changed";`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	_, source2, err := changed.MigrationBundle("202609100001_create_widget")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source1, source2) {
		t.Fatal("unrelated application source changed migration checksum authority")
	}
	if err := os.WriteFile(filepath.Join(directory, "shared.js"), []byte(`exports.value={ok:false};`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	_, source3, err := changed.MigrationBundle("202609100001_create_widget")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(source1, source3) {
		t.Fatal("declared migration dependency did not change checksum authority")
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
