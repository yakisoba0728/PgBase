package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/buildinfo"
)

func TestRunVersionEmitsStableNetworkFreeJSON(t *testing.T) {
	var stdout bytes.Buffer
	if err := runVersion(context.Background(), []string{"--json"}, emptyEnv, testIO(nil, &stdout, io.Discard)); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != buildinfo.Current().Version || got["goos"] == "" || got["goarch"] == "" || got["commit"] == "" {
		t.Fatalf("version JSON=%v", got)
	}
	if err := runVersion(context.Background(), nil, emptyEnv, testIO(nil, io.Discard, io.Discard)); err == nil {
		t.Fatal("missing --json accepted")
	}
}

func TestUpdateInputRequiresExplicitTrustAndSourceMode(t *testing.T) {
	for _, args := range [][]string{
		{"--source", "https://example.test/manifest.json"},
		{"--manifest", "/tmp/manifest.json", "--trust-key", "/tmp/key"},
		{"--source", "https://example.test/manifest.json", "--manifest", "/tmp/manifest.json", "--signature", "/tmp/manifest.sig", "--trust-key", "/tmp/key"},
	} {
		if _, err := parseUpdateInput("check", args); err == nil {
			t.Fatalf("accepted args=%v", args)
		}
	}
	input, err := parseUpdateInput("check", []string{"--source", "https://example.test/manifest.json", "--trust-key", "/tmp/key", "--max-download", "2MiB"})
	if err != nil || input.maxDownload != 2<<20 {
		t.Fatalf("input=%+v err=%v", input, err)
	}
}

func TestLoadTrustKeyRejectsSymlinkAndAcceptsExactBase64(t *testing.T) {
	directory := t.TempDir()
	key := bytes.Repeat([]byte("k"), 32)
	path := filepath.Join(directory, "trust.key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadTrustKey(path)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("key=%x err=%v", got, err)
	}
	if err := os.Symlink(path, filepath.Join(directory, "link")); err == nil {
		if _, linkErr := loadTrustKey(filepath.Join(directory, "link")); linkErr == nil {
			t.Fatal("symlink trust key accepted")
		}
	}
}
