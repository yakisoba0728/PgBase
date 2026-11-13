package jsvm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

type nativeHost struct{ calls []string }

func (h *nativeHost) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method)
	if method == "app-control.databaseBegin" {
		return json.RawMessage(`{"token":"owned-transaction"}`), nil
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func nativeRuntime(t *testing.T, source string) (*jsvm.Runtime, jsvm.HandlerRef) {
	t.Helper()
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v2","routes":[{"method":"GET","pattern":"/native","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", source)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime, bundle.Manifest().Routes[0].Handler
}

func TestNativeRuntimeUsesServicesWithoutCompatibilityGlobals(t *testing.T) {
	runtime, handler := nativeRuntime(t, `
if(typeof $app!=='undefined'||typeof Record!=='undefined'||typeof $os!=='undefined'||typeof routerAdd!=='undefined')throw new Error('legacy globals during validation');
const pg=require('pgbase');exports.run=input=>({records:pg.records.list({collection:'items'}),schema:pg.schema.export({}),input,legacy:typeof $app});`)
	host := &nativeHost{}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: handler, Capabilities: []string{"records", "app-control"}, Host: host, Input: json.RawMessage(`{"title":"original"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Input  map[string]any `json:"input"`
		Legacy string         `json:"legacy"`
	}
	if err := json.Unmarshal(output, &result); err != nil || !reflect.DeepEqual(result.Input, map[string]any{"title": "original"}) || result.Legacy != "undefined" {
		t.Fatalf("native response changed input or installed legacy globals: %s, %v", output, err)
	}
	if !reflect.DeepEqual(host.calls, []string{"records.list", "app-control.schemaExport"}) {
		t.Fatalf("native service calls = %v", host.calls)
	}
}

func TestNativeServiceCapabilityChecksRemainAtTheHostBoundary(t *testing.T) {
	runtime, handler := nativeRuntime(t, `const pg=require('pgbase');exports.run=()=>{try{pg.schema.export({})}catch(error){return {code:error.code}}}`)
	host := &nativeHost{}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: handler, Capabilities: []string{"records"}, Host: host})
	if err != nil || string(output) != `{"code":"js_capability_denied"}` || len(host.calls) != 0 {
		t.Fatalf("capability escape: %s, calls=%v, %v", output, host.calls, err)
	}
}

func TestNativeTransactionCommitsOnceOrRollsBackItsWholeCallback(t *testing.T) {
	for _, test := range []struct {
		name, callback string
		calls          []string
	}{
		{"nested commit", `api=>{api.records.create({collection:'items',input:{title:'one'}});return api.app.transaction(inner=>inner===api)}`, []string{"app-control.databaseBegin", "records.create", "app-control.databaseCommit"}},
		{"callback failure", `api=>{api.records.create({collection:'items',input:{title:'one'}});throw new Error('rollback')}`, []string{"app-control.databaseBegin", "records.create", "app-control.databaseRollback"}},
		{"async rejected", `async api=>{api.records.create({collection:'items',input:{title:'one'}})}`, []string{"app-control.databaseBegin", "records.create", "app-control.databaseRollback"}},
		{"thenable rejected", `api=>({then:()=>{}})`, []string{"app-control.databaseBegin", "app-control.databaseRollback"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, handler := nativeRuntime(t, `const pg=require('pgbase');exports.run=()=>{try{return {result:pg.app.transaction(`+test.callback+`)}}catch(error){return {failed:true}}}`)
			host := &nativeHost{}
			output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: handler, Capabilities: []string{"records", "app-control"}, Host: host})
			if err != nil || !reflect.DeepEqual(host.calls, test.calls) {
				t.Fatalf("transaction = %s, calls=%v, %v", output, host.calls, err)
			}
			if test.name == "nested commit" && string(output) != `{"result":true}` || test.name != "nested commit" && string(output) != `{"failed":true}` {
				t.Fatalf("transaction result = %s", output)
			}
		})
	}
}

func TestNativeBootstrapRegistersOnlyOnItsModule(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v2","bootstrap":"bootstrap.js","routes":[{"method":"GET","pattern":"/native","access":"public","handler":{"module":"placeholder.js","export":"run"}}]}`)
	writeFile(t, directory, "placeholder.js", `exports.run=()=>({})`)
	writeFile(t, directory, "bootstrap.js", `if(typeof routerAdd!=='undefined')throw new Error('legacy registration');const pg=require('pgbase');pg.registration.route('GET','/native',input=>({title:input.title,native:typeof $app==='undefined'}));`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(t.Context())
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Input: json.RawMessage(`{"title":"native"}`)})
	if err != nil || string(output) != `{"native":true,"title":"native"}` {
		t.Fatalf("native bootstrap = %s, %v", output, err)
	}
}

func TestMigrationSourcePreservesItsRuntimeFormatAndLegacyBytes(t *testing.T) {
	for _, format := range []string{jsvm.LegacyManifestFormat, jsvm.ManifestFormat} {
		directory := t.TempDir()
		writeFile(t, directory, "pgbase.scripts.json", `{"format":"`+format+`","migrations":[{"id":"202611130001_mode","module":"migration.js","up":"up","down":"down"}]}`)
		writeFile(t, directory, "migration.js", `exports.up=()=>({});exports.down=()=>({});`)
		bundle, err := jsvm.LoadBundle(directory)
		if err != nil {
			t.Fatal(err)
		}
		subset, source, err := bundle.MigrationBundle("202611130001_mode")
		if err != nil || subset.Manifest().Format != format {
			t.Fatalf("migration lost runtime format: %s, %v", source, err)
		}
		if format == jsvm.LegacyManifestFormat {
			if bytes.Contains(source, []byte(`"runtimeFormat"`)) || !bytes.Contains(source, []byte(`"format":"pgbase.js-migration-source/v1"`)) {
				t.Fatalf("historical envelope changed: %s", source)
			}
		} else if !strings.Contains(string(source), `"runtimeFormat":"pgbase.scripts/v2"`) || !strings.Contains(string(source), `"format":"pgbase.js-migration-source/v2"`) {
			t.Fatalf("native migration has no recorded runtime: %s", source)
		}
	}
}

func TestHandlerValidationInterruptsTopLevelLoop(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v2","routes":[{"method":"GET","pattern":"/loop","access":"public","handler":{"module":"loop.js","export":"run"}}]}`)
	writeFile(t, directory, "loop.js", `while(true){};exports.run=()=>({});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
		if runtime != nil {
			_ = runtime.Close(context.Background())
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted looping module")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler validation did not interrupt top-level loop")
	}
}

func TestMixedMigrationPreservesLegacyEnvelopeAndValidationMode(t *testing.T) {
	directory := t.TempDir()
	migration := `{"id":"202611130001_legacy","module":"legacy.js","up":"up","down":"down"}`
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","migrations":[`+migration+`]}`)
	writeFile(t, directory, "legacy.js", `if(typeof $app==='undefined')throw new Error('legacy mode lost');exports.up=()=>({});exports.down=()=>({});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	_, oldSource, err := bundle.MigrationBundle("202611130001_legacy")
	if err != nil {
		t.Fatal(err)
	}
	migration = strings.TrimSuffix(migration, "}") + `,"runtimeFormat":"pgbase.scripts/v1"}`
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v2","migrations":[`+migration+`,{"id":"202611130002_native","module":"native.js","up":"up","down":"down"}]}`)
	writeFile(t, directory, "native.js", `if(typeof $app!=='undefined')throw new Error('native mode lost');exports.up=()=>({});exports.down=()=>({});`)
	bundle, err = jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	_, source, err := bundle.MigrationBundle("202611130001_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, oldSource) {
		t.Fatalf("legacy source changed: %s -> %s", oldSource, source)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(t.Context())
}
