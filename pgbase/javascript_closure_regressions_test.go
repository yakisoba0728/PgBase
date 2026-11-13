package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestJavaScriptLocalWriterPaddedChunksAndFilesystemOnlyUpload(t *testing.T) {
	root := t.TempDir()
	host := newExtensionHostPolicy(JavaScriptHostOptions{FilesystemRoot: root})
	// Each write is independent base64; padding must not terminate the next chunk.
	_, err := host.call(t.Context(), "filesystem.localWrite", json.RawMessage(`{"path":"chunks","chunks":["YQ==","Yg=="]}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "chunks"))
	if err != nil || string(data) != "ab" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	directory := t.TempDir()
	os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/fs","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem"]}]}`), 0600)
	os.WriteFile(filepath.Join(directory, "route.js"), []byte(`exports.run=()=>{const fs=$filesystem.local(".");fs.upload("YQ==","upload");fs.upload("plain text","text");fs.upload([98],"bytes");const w=fs.newWriter("writer");w.write("YQ==");w.write("Yg==");w.close();return {ok:true}}`), 0600)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	bridge := &extensionBridge{hostPolicy: host}
	_, err = runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"filesystem"}, Host: &extensionHost{bridge: bridge, valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "writer"))
	if err != nil || string(data) != "ab" {
		t.Fatalf("writer=%q err=%v", data, err)
	}
}

func TestJavaScriptEnrichPatchPreservesExactNumber(t *testing.T) {
	event := &HookEvent{Kind: "record.enrich", Data: &api.RecordEnrichEvent{Record: map[string]any{"value": nil}}}
	if err := applyJavaScriptHookPatch(event, json.RawMessage(`{"record":{"value":9007199254740993}}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(event.Data.(*api.RecordEnrichEvent).Record)
	if string(raw) != `{"value":9007199254740993}` {
		t.Fatalf("patch=%s", raw)
	}
}

func TestExtensionChunkQuotaAllowsEOFButRejectsExtraByte(t *testing.T) {
	for _, tail := range []string{"", "x"} {
		id := uuid.New()
		host := &extensionHost{fileBytesRead: maximumExtensionFileBytes, fileHandles: map[uuid.UUID]extensionFileHandle{id: {reader: io.NopCloser(strings.NewReader(tail))}}}
		raw, err := host.readFileChunk(extensionFileRequest{Handle: id.String()})
		if (err == nil) != (tail == "") {
			t.Fatalf("file tail=%q raw=%s err=%v", tail, raw, err)
		}
		objects := &extensionHost{bridge: &extensionBridge{objects: &ExtensionObjects{maximumBytes: 1}}, objectBytesRead: 1, objectHandles: map[uuid.UUID]io.ReadCloser{id: io.NopCloser(strings.NewReader(tail))}}
		raw, err = objects.readObjectChunk(json.RawMessage(`{"handle":"` + id.String() + `"}`))
		if (err == nil) != (tail == "") {
			t.Fatalf("object tail=%q raw=%s err=%v", tail, raw, err)
		}
	}
}

func TestJavaScriptHookPreservesUserJSONKeys(t *testing.T) {
	raw, err := marshalJavaScriptHookData(&HookEvent{Kind: HookRecordCreateRequest, Data: &api.RecordRequestEvent{Input: map[string]json.RawMessage{"Payload": json.RawMessage(`{"URL":"keep","CustomerID":9007199254740993}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Input["Payload"]) != `{"CustomerID":9007199254740993,"URL":"keep"}` {
		t.Fatalf("user JSON changed: %s", raw)
	}
}

func TestJavaScriptLocalCopyRejectsOversizedInputWithoutReplacingDestination(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source"), []byte(strings.Repeat("x", maximumHostPolicyBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "destination"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	host := newExtensionHostPolicy(JavaScriptHostOptions{FilesystemRoot: root})
	if _, err := host.call(t.Context(), "filesystem.localCopy", json.RawMessage(`{"source":"source","destination":"destination"}`)); err == nil {
		t.Fatal("oversized copy accepted")
	}
	data, err := os.ReadFile(filepath.Join(root, "destination"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("destination=%q err=%v", data, err)
	}
}

func TestModelAndCollectionErrorAfterHooksReceiveOriginalFailure(t *testing.T) {
	failure := errors.New("original failure")
	for _, collection := range []bool{false, true} {
		registry := &HookRegistry{}
		seen := 0
		_, err := registry.On("model.create.error", Hook{After: func(_ context.Context, event *HookEvent) {
			if !errors.Is(event.Err, failure) {
				t.Errorf("After error=%v", event.Err)
			}
			seen++
		}})
		if err != nil {
			t.Fatal(err)
		}
		if collection {
			finish, err := (schemaLifecycleHooks{registry: registry}).BeginCollectionMutation(t.Context(), &schema.CollectionMutationEvent{Operation: "create"})
			if err != nil {
				t.Fatal(err)
			}
			finish(failure)
		} else {
			(&App{options: Options{Hooks: registry}}).observeMutation(t.Context(), &ModelMutationEvent{Operation: "create", Family: "record", Err: failure}, "error")
		}
		if seen != 1 {
			t.Fatalf("After calls=%d", seen)
		}
	}
}

func TestLegacyRawDatabaseRequiresIndependentCapabilityAndOption(t *testing.T) {
	host := &extensionHost{bridge: &extensionBridge{}, valid: true}
	for _, method := range []string{"app-control.databaseExec", "app-control.databaseTransaction"} {
		if _, err := host.Call(t.Context(), method, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("disabled database accepted %s", method)
		}
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/db","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `exports.run=()=>{try{$app.db().query("select 1")}catch(e){return {code:e.code}}return {code:"allowed"}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	raw, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control"}})
	if err != nil || !strings.Contains(string(raw), string(jsvm.CodeCapabilityDenied)) {
		t.Fatalf("database ceiling=%s err=%v", raw, err)
	}
}

func TestBackupFacadeResolvesOwnedAndInjectedManager(t *testing.T) {
	owned, injected := new(backup.Manager), new(backup.Manager)
	control := &extensionAppControl{app: &App{backupManager: owned, options: Options{Backups: BackupOptions{manager: injected}}}}
	if control.backupManager() != owned {
		t.Fatal("owned manager not selected")
	}
	control.app.backupManager = nil
	if control.backupManager() != injected {
		t.Fatal("injected manager unavailable")
	}
}

func TestRootedFilesystemRejectsExistingSiblingAndSymlink(t *testing.T) {
	root, directory := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte("private sibling"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret", filepath.Join(root, "assets", "link")); err != nil {
		t.Fatal(err)
	}
	policy := newExtensionHostPolicy(JavaScriptHostOptions{FilesystemRoot: root})
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/roots","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["filesystem","os"]}]}`, `exports.run=()=>{const local=$filesystem.local("assets"),root=$os.openRoot("assets");let denied=0;for(const read of [()=>local.getReader("../secret"),()=>local.getReader("link"),()=>local.serve(null,null,"../secret","secret"),()=>root.fs().readFile("../secret"),()=>root.fs().readFile("link"),()=>$os.openInRoot("assets","../secret"),()=>$os.dirFS("assets").readFile("link")]){try{read()}catch(_){denied++}}return {denied}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	raw, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"filesystem", "os"}, Host: &extensionHost{bridge: &extensionBridge{hostPolicy: policy}, valid: true}})
	if err != nil || string(raw) != `{"denied":7}` {
		t.Fatalf("subroot=%s err=%v", raw, err)
	}
}
