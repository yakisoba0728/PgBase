package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	oldCommit = "1111111111111111111111111111111111111111"
	newCommit = "2222222222222222222222222222222222222222"
)

func TestVerifyManifestRejectsBadSignatureAndDuplicateArtifact(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixtureManifest("v0.2.0", newCommit, "https://example.test/pgbase", 1, strings.Repeat("a", 64))
	manifest.Releases[0].Artifacts = append(manifest.Releases[0].Artifacts, manifest.Releases[0].Artifacts[0])
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := sign(private, raw)
	if _, err := VerifyManifest(raw, signature, public); !errors.Is(err, ErrUntrustedManifest) {
		t.Fatalf("duplicate artifact error=%v", err)
	}
	manifest = fixtureManifest("v0.2.0", newCommit, "https://example.test/pgbase", 1, strings.Repeat("a", 64))
	raw, _ = json.Marshal(manifest)
	signature = sign(private, raw)
	signature[0] ^= 1
	if _, err := VerifyManifest(raw, signature, public); !errors.Is(err, ErrUntrustedManifest) {
		t.Fatalf("tampered signature error=%v", err)
	}
}

func TestSelectEnforcesVersionChannelAndPlatform(t *testing.T) {
	manifest := Manifest{SchemaVersion: 1, Channel: "stable", Releases: []Release{
		fixtureRelease("v0.3.0-beta.1", newCommit, "https://example.test/a", 1, strings.Repeat("a", 64)),
		fixtureRelease("v0.2.0", newCommit, "https://example.test/b", 1, strings.Repeat("b", 64)),
		fixtureRelease("v0.3.0", oldCommit, "https://example.test/c", 1, strings.Repeat("c", 64)),
	}}
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	candidate, err := Select(manifest, Selection{Current: current})
	if err != nil || candidate.Release.Version != "v0.3.0" {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	if _, err = Select(manifest, Selection{Current: Build{Version: "dev", Commit: "unknown", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("dev latest error=%v", err)
	}
	if _, err = Select(manifest, Selection{Current: current, Requested: "v0.0.1"}); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("unknown exact error=%v", err)
	}
	manifest.Releases = []Release{fixtureRelease("v0.0.9", newCommit, "https://example.test/d", 1, strings.Repeat("d", 64))}
	if _, err = Select(manifest, Selection{Current: current, Requested: "v0.0.9"}); !errors.Is(err, ErrDowngradeRefused) {
		t.Fatalf("downgrade error=%v", err)
	}
}

func TestHTTPSFetchDownloadApplyAndRollbackNativeFixture(t *testing.T) {
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	install := t.TempDir()
	target := filepath.Join(install, "pgbase")
	copyFixture(t, oldBinary, target)
	newBytes, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := startTLSServer(t, func(base string) http.Handler {
		manifest := fixtureManifest("v0.2.0", newCommit, base+"/artifact", int64(len(newBytes)), checksum(newBytes))
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		signature := sign(private, raw)
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/manifest.json":
				_, _ = writer.Write(raw)
			case "/manifest.json.ed25519":
				_, _ = writer.Write(signature)
			case "/artifact":
				_, _ = writer.Write(newBytes)
			default:
				http.NotFound(writer, request)
			}
		})
	})
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	status, err := Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current})
	if err != nil || status.Phase != "committed" {
		t.Fatalf("apply status=%+v err=%v", status, err)
	}
	newBuild, err := ProbeBinary(context.Background(), target)
	if err != nil || newBuild.Version != "v0.2.0" || newBuild.Commit != newCommit {
		t.Fatalf("new=%+v err=%v", newBuild, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("target mode=%v err=%v", info.Mode(), err)
	}
	rolledBack, err := Rollback(context.Background(), target, filepath.Join(install, ".pgbase-update"), nil)
	if err != nil || rolledBack.Phase != "rolled_back" {
		t.Fatalf("rollback=%+v err=%v", rolledBack, err)
	}
	restored, err := ProbeBinary(context.Background(), target)
	if err != nil || restored.Version != "v0.1.0" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	status, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current})
	if err != nil || status.Phase != "committed" {
		t.Fatalf("retry status=%+v err=%v", status, err)
	}
	entry, err := loadJournal(filepath.Join(install, ".pgbase-update"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(install, ".pgbase-update", entry.Backup), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(context.Background(), target, filepath.Join(install, ".pgbase-update"), nil); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("tampered backup rollback error=%v", err)
	}
}

func TestApplyRollsBackCanonicalProbeFailure(t *testing.T) {
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "pgbase")
	install := t.TempDir()
	target := filepath.Join(install, "pgbase")
	copyFixture(t, oldBinary, target)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newBytes, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	server := startTLSServer(t, func(base string) http.Handler {
		manifest := fixtureManifest("v0.2.0", newCommit, base+"/artifact", int64(len(newBytes)), checksum(newBytes))
		raw, _ := json.Marshal(manifest)
		signature := sign(private, raw)
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/manifest.json" {
				_, _ = writer.Write(raw)
				return
			}
			if request.URL.Path == "/manifest.json.ed25519" {
				_, _ = writer.Write(signature)
				return
			}
			if request.URL.Path == "/artifact" {
				_, _ = writer.Write(newBytes)
				return
			}
			http.NotFound(writer, request)
		})
	})
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current}); !errors.Is(err, ErrProbeFailed) {
		t.Fatalf("apply error=%v", err)
	}
	restored, err := ProbeBinary(context.Background(), target)
	if err != nil || restored.Version != "v0.1.0" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestInterruptedDownloadAndRecoveryNeverReplaceOriginal(t *testing.T) {
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	newBytes, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	install := t.TempDir()
	target := filepath.Join(install, "pgbase")
	copyFixture(t, oldBinary, target)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := startTLSServer(t, func(base string) http.Handler {
		manifest := fixtureManifest("v0.2.0", newCommit, base+"/artifact", int64(len(newBytes)), checksum(newBytes))
		raw, _ := json.Marshal(manifest)
		signature := sign(private, raw)
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/manifest.json":
				_, _ = writer.Write(raw)
			case "/manifest.json.ed25519":
				_, _ = writer.Write(signature)
			case "/artifact":
				_, _ = writer.Write(newBytes[:len(newBytes)/2])
			}
		})
	})
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("interrupted error=%v", err)
	}
	if currentBuild, probeErr := ProbeBinary(context.Background(), target); probeErr != nil || !sameBuild(currentBuild, current) {
		t.Fatalf("original changed=%+v err=%v", currentBuild, probeErr)
	}
	status, recoverErr := Rollback(context.Background(), target, filepath.Join(install, ".pgbase-update"), nil)
	if recoverErr != nil || status.Phase != "aborted" {
		t.Fatalf("interrupted recovery status=%+v err=%v", status, recoverErr)
	}
}

func TestApplyRejectsSymlinkAndConcurrentLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission differs on Windows")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "pgbase")
	if err := os.Symlink("missing", target); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareInstallation(target, filepath.Join(directory, ".pgbase-update")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("symlink error=%v", err)
	}
	regular := buildFixture(t, "v0.1.0", oldCommit, "")
	os.Remove(target)
	copyFixture(t, regular, target)
	state, _, first, err := prepareInstallation(target, filepath.Join(directory, ".pgbase-update"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err = acquireLock(first); err != nil {
		t.Fatal(err)
	}
	defer releaseLock(first)
	_, _, second, err := prepareInstallation(target, state)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = acquireLock(second); !errors.Is(err, ErrReplaceBusy) {
		t.Fatalf("lock error=%v", err)
	}
}

func fixtureManifest(version, commit, artifactURL string, size int64, digest string) Manifest {
	return Manifest{SchemaVersion: 1, Channel: "stable", Releases: []Release{fixtureRelease(version, commit, artifactURL, size, digest)}}
}
func fixtureRelease(version, commit, artifactURL string, size int64, digest string) Release {
	return Release{Version: version, Commit: commit, MinUpdaterVersion: "v0.1.0", Schema: Schema{Min: 40, Max: 40}, Artifacts: []Artifact{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: artifactURL, Size: size, SHA256: digest}}}
}
func sign(key ed25519.PrivateKey, raw []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw)))
}
func checksum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func startTLSServer(t *testing.T, handler func(string) http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:58098")
	if err != nil {
		t.Fatalf("reserve N9 TLS port: %v", err)
	}
	server := httptest.NewUnstartedServer(handler("https://" + listener.Addr().String()))
	server.Listener = listener
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

var fixtureBuildMu sync.Mutex

func buildFixture(t *testing.T, version, commit, failName string) string {
	t.Helper()
	fixtureBuildMu.Lock()
	defer fixtureBuildMu.Unlock()
	output := filepath.Join(t.TempDir(), "fixture")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	arguments := []string{"build", "-trimpath", "-ldflags", "-X main.version=" + version + " -X main.commit=" + commit + " -X main.goos=" + runtime.GOOS + " -X main.goarch=" + runtime.GOARCH + " -X main.failName=" + failName, "-o", output, "./internal/selfupdate/testdata/fixture"}
	command := exec.Command("go", arguments...)
	command.Dir = repositoryRoot(t)
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, outputBytes)
	}
	return output
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(output, input); err != nil {
		t.Fatal(err)
	}
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadHonorsCancellation(t *testing.T) {
	context, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Timeout: time.Second}
	if _, err := Fetch(context, FetchOptions{Source: "https://example.test/manifest.json", TrustKey: make([]byte, ed25519.PublicKeySize), Current: Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, HTTPClient: client}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestFetchRejectsCrossOriginRedirectWithoutContactingTarget(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		foreignCalls++
		writer.WriteHeader(http.StatusOK)
	}))
	defer foreign.Close()
	primary := startTLSServer(t, func(string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, foreign.URL+"/manifest.json", http.StatusFound)
		})
	})
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if _, err := Fetch(context.Background(), FetchOptions{Source: primary.URL + "/manifest.json", TrustKey: make([]byte, ed25519.PublicKeySize), Current: current, HTTPClient: primary.Client()}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("redirect error=%v", err)
	}
	if foreignCalls != 0 {
		t.Fatalf("cross-origin target received %d requests", foreignCalls)
	}
}

func TestFetchRejectsSignedArtifactAboveCallerLimit(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := startTLSServer(t, func(base string) http.Handler {
		manifest := fixtureManifest("v0.2.0", newCommit, base+"/artifact", MinimumArtifactMax+1, strings.Repeat("a", 64))
		raw, marshalErr := json.Marshal(manifest)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		signature := sign(private, raw)
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/manifest.json" {
				_, _ = writer.Write(raw)
				return
			}
			if request.URL.Path == "/manifest.json.ed25519" {
				_, _ = writer.Write(signature)
				return
			}
			http.NotFound(writer, request)
		})
	})
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if _, err = Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client(), MaxArtifactBytes: MinimumArtifactMax}); !errors.Is(err, ErrDownloadLimit) {
		t.Fatalf("limit error=%v", err)
	}
}
