package jsvm_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadBundleRejectsUndeclaredMigrationDependencyClosure(t *testing.T) {
	for _, test := range []struct {
		name       string
		entry      string
		declared   string
		undeclared string
	}{
		{name: "nested dependency", entry: `const declared=require("./declared"); exports.up=()=>declared.run(); exports.down=()=>({});`,
			declared: `const hidden=require("./hidden"); exports.run=()=>hidden.run();`, undeclared: `exports.run=()=>({});`},
		{name: "down only dependency", entry: `exports.up=()=>({}); exports.down=()=>require("./hidden").run();`,
			declared: `exports.run=()=>({});`, undeclared: `exports.run=()=>({});`},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100030_closure","module":"migration.js","up":"up","down":"down","dependencies":["declared.js"]}]}`)
			writeFile(t, directory, "migration.js", test.entry)
			writeFile(t, directory, "declared.js", test.declared)
			writeFile(t, directory, "hidden.js", test.undeclared)
			if _, err := jsvm.LoadBundle(directory); err == nil || !strings.Contains(err.Error(), "undeclared dependency") {
				t.Fatalf("LoadBundle undeclared closure error = %v", err)
			}
		})
	}
}

func TestLoadBundleRejectsDynamicRequireInMigrationSource(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100031_dynamic","module":"migration.js","up":"up","down":"down"}]}`)
	writeFile(t, directory, "migration.js", `exports.up=()=>({}); exports.down=()=>{const name="./hidden";return require(name).run();};`)
	writeFile(t, directory, "hidden.js", `exports.run=()=>({});`)
	if _, err := jsvm.LoadBundle(directory); err == nil || !strings.Contains(err.Error(), "dynamic require") {
		t.Fatalf("LoadBundle dynamic require error = %v", err)
	}
}

func TestBundleWriteSnapshotPreservesLoadedBytes(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/snapshot","access":"public","handler":{"module":"app.js","export":"run"}}]}`)
	writeFile(t, directory, "app.js", `exports.run=()=>({status:204});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, directory, "app.js", `throw new Error("changed");`)
	snapshot, err := bundle.WriteSnapshot("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(snapshot) })
	loaded, err := jsvm.LoadBundle(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest() != bundle.Digest() || string(loaded.Source("app.js")) != `exports.run=()=>({status:204});` {
		t.Fatal("snapshot did not preserve the loaded bundle")
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
