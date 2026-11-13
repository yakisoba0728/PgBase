package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yakisoba0728/PgBase/pgbase"
)

func TestJavaScriptWatchTreatsCanceledNilRunAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fmtWatchedApplicationStopped(ctx, nil); err != nil {
		t.Fatalf("canceled watch returned %v", err)
	}
}

func TestJavaScriptWatchReportsUnexpectedNilRun(t *testing.T) {
	err := fmtWatchedApplicationStopped(context.Background(), nil)
	if err == nil || !errors.Is(err, errWatchedApplicationStopped) {
		t.Fatalf("unexpected nil run error=%v", err)
	}
}

func TestJavaScriptWatchPreservesRunError(t *testing.T) {
	runErr := errors.New("serve failed")
	if err := fmtWatchedApplicationStopped(context.Background(), runErr); !errors.Is(err, runErr) {
		t.Fatalf("watch error=%v, want wrapped run error", err)
	}
}

func TestJavaScriptWatchSnapshotPreservesNativeRuntimeSelection(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v2","routes":[{"method":"GET","pattern":"/native","access":"public","handler":{"module":"app.js","export":"hello"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte(`if(typeof $app!=="undefined")throw new Error("legacy globals in native snapshot"); exports.hello=()=>({status:200,json:{ok:true}});`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := createJavaScriptSnapshot(&pgbase.JavaScriptOptions{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(snapshot.directory)
	manifest, err := pgbase.ValidateJavaScript(context.Background(), pgbase.JavaScriptOptions{Directory: snapshot.directory})
	if err != nil || manifest.Format != "pgbase.scripts/v2" || manifest.Digest != snapshot.digest {
		t.Fatalf("native snapshot format/digest=%s/%s, %v", manifest.Format, manifest.Digest, err)
	}
}
