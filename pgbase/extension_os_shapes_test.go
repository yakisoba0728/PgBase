package pgbase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestPocketBaseOSShapesUseRootedRealHostEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo fixture unavailable")
	}
	root, bundleDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input.bin"), []byte{0, 1, 127, 128, 255}, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/os","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["os"]}]}`
	source := `exports.run=()=>{const info=$os.stat("input.bin"),entries=$os.readDir("."),cmd=$os.cmd("echo","deferred"),before=$os.readFile("input.bin"),output=cmd.output(),root=$os.openRoot(".");const nested=root.fs().readFile("input.bin");root.close();let closed=false;try{root.fs().stat("input.bin")}catch(e){closed=true}return {name:info.name(),size:info.size(),dir:info.isDir(),entry:entries[0].name(),bytes:Array.from(before),nested:Array.from(nested),command:Array.from(output),isCommand:cmd instanceof Command,temp:$os.tempDir(),closed}}`
	for name, data := range map[string]string{"pgbase.scripts.json": manifest, "route.js": source} {
		if err := os.WriteFile(filepath.Join(bundleDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := jsvm.LoadBundle(bundleDir)
	if err != nil {
		t.Fatalf("%v unwrap=%v", err, errors.Unwrap(err))
	}
	engine, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatalf("%v unwrap=%v", err, errors.Unwrap(err))
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })
	options, err := (PocketBaseCompatibilityOptions{FilesystemRoot: root, Commands: map[string]string{"echo": "/bin/echo"}}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: &extensionBridge{compatibility: newExtensionCompatibility(options)}, valid: true}
	out, err := engine.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"os"}, Host: host})
	if err != nil {
		t.Fatalf("%v unwrap=%v", err, errors.Unwrap(err))
	}
	text := string(out)
	for _, want := range []string{`"bytes":[0,1,127,128,255]`, `"nested":[0,1,127,128,255]`, `"size":5`, `"isCommand":true`, `"closed":true`, `100,101,102,101,114,114,101,100,10`} {
		if !strings.Contains(text, want) {
			t.Fatalf("output=%s missing=%s", text, want)
		}
	}
}

func TestPocketBaseOSRejectsUnlistedCommandAndCancelledInvocation(t *testing.T) {
	options, _ := (PocketBaseCompatibilityOptions{FilesystemRoot: t.TempDir(), Commands: map[string]string{"echo": "/bin/echo"}}).normalized()
	adapter := newExtensionCompatibility(options)
	if _, err := adapter.call(t.Context(), "os.cmd", []byte(`{"name":"sh","args":["-c","exit 0"]}`)); err == nil {
		t.Fatal("unlisted command executed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	raw, err := adapter.call(ctx, "os.cmd", []byte(`{"name":"echo","args":["cancelled"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"success":true`) {
		t.Fatal("cancelled command executed")
	}
}
