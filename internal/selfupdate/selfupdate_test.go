package selfupdate

import (
	"bytes"
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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	oldCommit = "1111111111111111111111111111111111111111"
	newCommit = "2222222222222222222222222222222222222222"
)

func TestCopyRegularDoesNotRemoveAnExistingDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(source, target, 0o700); err == nil {
		t.Fatal("copyRegular replaced an existing destination")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("existing destination = %q", got)
	}
}

func TestSameBuildIncludesSchemaCompatibilityRange(t *testing.T) {
	build := Build{Version: "v1.2.3", Commit: newCommit, GOOS: "linux", GOARCH: "amd64", SchemaMin: "4", SchemaMax: "6"}
	if !sameBuild(build, build) {
		t.Fatal("identical builds are different")
	}
	for _, candidate := range []Build{
		{Version: build.Version, Commit: build.Commit, GOOS: build.GOOS, GOARCH: build.GOARCH},
		{Version: build.Version, Commit: build.Commit, GOOS: build.GOOS, GOARCH: build.GOARCH, SchemaMin: "3", SchemaMax: "6"},
		{Version: build.Version, Commit: build.Commit, GOOS: build.GOOS, GOARCH: build.GOARCH, SchemaMin: "4", SchemaMax: "7"},
	} {
		if sameBuild(candidate, build) {
			t.Fatalf("candidate range %s..%s matched signed range %s..%s", candidate.SchemaMin, candidate.SchemaMax, build.SchemaMin, build.SchemaMax)
		}
	}
}

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

func TestVerifyManifestRejectsDuplicateKeysAndMalformedUnselectedArtifacts(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixtureManifest("v0.2.0", newCommit, "https://example.test/pgbase", 1, strings.Repeat("a", 64))
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(raw, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1)
	if _, err = VerifyManifest(duplicate, sign(private, duplicate), public); !errors.Is(err, ErrUntrustedManifest) {
		t.Fatalf("duplicate key error=%v", err)
	}
	manifest.Releases[0].Artifacts[0].URL = "https://example.test/artifact#fragment"
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyManifest(raw, sign(private, raw), public); !errors.Is(err, ErrUntrustedManifest) {
		t.Fatalf("malformed artifact error=%v", err)
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

func TestSelectRejectsUnverifiableSourceMinimumAndOrdersLargePrereleases(t *testing.T) {
	manifest := fixtureManifest("v0.2.0", newCommit, "https://example.test/pgbase", 1, strings.Repeat("a", 64))
	manifest.Releases[0].MinUpdaterVersion = "v99.0.0"
	if _, err := Select(manifest, Selection{Current: Build{Version: "dev", Commit: "unknown", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Requested: "v0.2.0"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("source minimum error=%v", err)
	}
	manifest = Manifest{SchemaVersion: 1, Channel: "beta", Releases: []Release{
		fixtureRelease("v1.0.0-18446744073709551616", newCommit, "https://example.test/first", 1, strings.Repeat("a", 64)),
		fixtureRelease("v1.0.0-18446744073709551617", newCommit, "https://example.test/second", 1, strings.Repeat("b", 64)),
	}}
	for index := range manifest.Releases {
		manifest.Releases[index].MinUpdaterVersion = "v0.1.0"
	}
	candidate, err := Select(manifest, Selection{Current: Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Channel: "beta"})
	if err != nil || candidate.Release.Version != "v1.0.0-18446744073709551617" {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
}

func TestHTTPSFetchDownloadApplyAndRollbackNativeFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows post-exit replacement is covered by the native helper acceptance test")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	install := t.TempDir()
	target := fixtureTargetPath(install)
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

func TestLocalSignedArtifactApplyAndRollbackFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file URL syntax and replacement timing are platform-specific")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	install := t.TempDir()
	target := fixtureTargetPath(install)
	copyFixture(t, oldBinary, target)
	artifact, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixtureManifest("v0.2.0", newCommit, "file://"+newBinary, int64(len(artifact)), checksum(artifact))
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(install, "manifest.json")
	signaturePath := manifestPath + ".ed25519"
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(signaturePath, sign(private, raw), 0600); err != nil {
		t.Fatal(err)
	}
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: manifestPath, SignaturePath: signaturePath, TrustKey: public, Current: current})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current}); err != nil {
		t.Fatal(err)
	}
	if current, probeErr := ProbeBinary(context.Background(), target); probeErr != nil || current.Version != "v0.2.0" {
		t.Fatalf("current=%+v err=%v", current, probeErr)
	}
	if _, err = Rollback(context.Background(), target, filepath.Join(install, ".pgbase-update"), nil); err != nil {
		t.Fatal(err)
	}
	if current, probeErr := ProbeBinary(context.Background(), target); probeErr != nil || current.Version != "v0.1.0" {
		t.Fatalf("current=%+v err=%v", current, probeErr)
	}
	if err = os.Symlink(newBinary, filepath.Join(install, "artifact-link")); err == nil {
		manifest.Releases[0].Artifacts[0].URL = "file://" + filepath.Join(install, "artifact-link")
		raw, _ = json.Marshal(manifest)
		_ = os.WriteFile(manifestPath, raw, 0600)
		_ = os.WriteFile(signaturePath, sign(private, raw), 0600)
		linked, fetchErr := Fetch(context.Background(), FetchOptions{Source: manifestPath, SignaturePath: signaturePath, TrustKey: public, Current: current, Requested: "v0.2.0"})
		if fetchErr != nil {
			// Fetch authenticates metadata; symlink rejection occurs on download.
			t.Fatal(fetchErr)
		}
		destination := filepath.Join(install, "linked-candidate")
		if _, downloadErr := linked.DownloadArtifact(context.Background(), destination); !errors.Is(downloadErr, ErrInvalidInput) {
			t.Fatalf("symlink download error=%v", downloadErr)
		}
		if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
			t.Fatalf("symlink download left destination: %v", statErr)
		}
	}
}

func TestApplyRejectsCandidateNotBoundToSignedManifestBeforeEffects(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	manifest := fixtureManifest("v0.2.0", newCommit, "file://"+filepath.Join(directory, "signed-artifact"), 1, strings.Repeat("a", 64))
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Select(manifest, Selection{Current: current})
	if err != nil {
		t.Fatal(err)
	}
	candidate.Artifact.URL = "file://" + filepath.Join(directory, "unsigned-artifact")
	state := filepath.Join(directory, ".pgbase-update")
	target := fixtureTargetPath(directory)
	if err := os.WriteFile(target, []byte("existing executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(context.Background(), ApplyOptions{
		Target: target, StateDir: state, TrustKey: public, Current: current,
		Fetched: Fetched{ManifestRaw: raw, SignatureRaw: sign(private, raw), Candidate: candidate, local: true},
	})
	if !errors.Is(err, ErrUntrustedManifest) {
		t.Fatalf("apply error=%v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(state, "journal.json")); !os.IsNotExist(statErr) {
		t.Fatalf("rejected candidate wrote journal evidence: %v", statErr)
	}
}

func TestApplyCrashPhasesRecoverDeterministically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synchronous replacement phases are Unix-specific")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	artifact, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ phase, want string }{
		{"manifest_verified", "aborted"}, {"staged", "aborted"}, {"backup_created", "aborted"}, {"replacement_started", "rolled_back"}, {"replacement_visible", "committed"},
	} {
		t.Run(test.phase, func(t *testing.T) {
			directory := t.TempDir()
			target := fixtureTargetPath(directory)
			copyFixture(t, oldBinary, target)
			manifest := fixtureManifest("v0.2.0", newCommit, "file://"+newBinary, int64(len(artifact)), checksum(artifact))
			raw, _ := json.Marshal(manifest)
			manifestPath := filepath.Join(directory, "manifest.json")
			signaturePath := manifestPath + ".ed25519"
			if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(signaturePath, sign(private, raw), 0600); err != nil {
				t.Fatal(err)
			}
			current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
			fetched, err := Fetch(context.Background(), FetchOptions{Source: manifestPath, SignaturePath: signaturePath, TrustKey: public, Current: current})
			if err != nil {
				t.Fatal(err)
			}
			_, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(directory, ".pgbase-update"), Fetched: fetched, TrustKey: public, Current: current, BeforePhase: func(phase string) error {
				if phase == test.phase {
					return errors.New("crash")
				}
				return nil
			}})
			if err == nil {
				t.Fatal("crash seam did not interrupt")
			}
			status, err := Recover(context.Background(), target, filepath.Join(directory, ".pgbase-update"), nil)
			if err != nil || status.Phase != test.want {
				t.Fatalf("status=%+v err=%v", status, err)
			}
		})
	}
}

func TestApplyRollsBackCanonicalProbeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rollback-after-health-check is covered by the native helper acceptance test")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "pgbase")
	install := t.TempDir()
	target := fixtureTargetPath(install)
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
	if runtime.GOOS == "windows" {
		t.Skip("local file URL syntax is platform-specific")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	newBytes, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	install := t.TempDir()
	target := fixtureTargetPath(install)
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
	target := fixtureTargetPath(directory)
	if err := os.Symlink("missing", target); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareInstallation(target, filepath.Join(directory, ".pgbase-update")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("symlink error=%v", err)
	}
	regular := buildFixture(t, "v0.1.0", oldCommit, "")
	os.Remove(target)
	copyFixture(t, regular, target)
	statePath := filepath.Join(directory, ".pgbase-update")
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	lockTarget := filepath.Join(directory, "other-lock")
	if err := os.WriteFile(lockTarget, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lockTarget, filepath.Join(statePath, "lock")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareInstallation(target, statePath); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("symlink lock error=%v", err)
	}
	if err := os.Remove(filepath.Join(statePath, "lock")); err != nil {
		t.Fatal(err)
	}
	state, _, first, err := prepareInstallation(target, statePath)
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

func TestRecoverRollbackStartedAtEveryPublicationEdge(t *testing.T) {
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	for _, test := range []struct {
		name          string
		targetIsOld   bool
		rollbackStage bool
	}{
		{name: "before_copy"},
		{name: "after_copy", rollbackStage: true},
		{name: "after_rename", targetIsOld: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := fixtureTargetPath(directory)
			if test.targetIsOld {
				copyFixture(t, oldBinary, target)
			} else {
				copyFixture(t, newBinary, target)
			}
			state := filepath.Join(directory, ".pgbase-update")
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			backup := "backup-test"
			if runtime.GOOS == "windows" {
				backup += ".exe"
			}
			copyFixture(t, oldBinary, filepath.Join(state, backup))
			oldDigest, _, err := fileDigestAndMode(oldBinary)
			if err != nil {
				t.Fatal(err)
			}
			newDigest, _, err := fileDigestAndMode(newBinary)
			if err != nil {
				t.Fatal(err)
			}
			entry := journal{SchemaVersion: 1, Phase: "rollback_started", Target: filepath.Base(target), Candidate: ".pgbase-candidate-test", Backup: backup,
				Old: Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, New: Build{Version: "v0.2.0", Commit: newCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
				OldSHA256: oldDigest, NewSHA256: newDigest, ManifestSHA256: strings.Repeat("a", 64), SignatureSHA256: strings.Repeat("b", 64), TrustFingerprint: strings.Repeat("c", 64)}
			if test.rollbackStage {
				copyFixture(t, oldBinary, filepath.Join(directory, ".pgbase.pgbase-rollback"))
			}
			if err = writeJournal(state, entry); err != nil {
				t.Fatal(err)
			}
			status, err := Recover(context.Background(), target, state, nil)
			if err != nil || status.Phase != "rolled_back" {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			if !status.Recovered {
				t.Fatal("recovery transition was not marked")
			}
			current, err := ProbeBinary(context.Background(), target)
			if err != nil || !sameBuild(current, entry.Old) {
				t.Fatalf("current=%+v err=%v", current, err)
			}
		})
	}
}

func TestPrepareInstallationRejectsWorldWritableExistingState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX state permissions are exercised on Unix")
	}
	directory := t.TempDir()
	target := fixtureTargetPath(directory)
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)
	state := filepath.Join(directory, ".pgbase-update")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0777); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := prepareInstallation(target, state); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("world-writable state error=%v", err)
	}
}

func TestReadStatusFirstUseReturnsIdleWithoutCreatingState(t *testing.T) {
	directory := t.TempDir()
	target := fixtureTargetPath(directory)
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)
	state := filepath.Join(directory, ".pgbase-update")
	status, err := ReadStatus(target, state)
	if err != nil || status.Phase != "idle" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err = os.Lstat(state); !os.IsNotExist(err) {
		t.Fatalf("ReadStatus created state: %v", err)
	}
}

func TestPrepareInstallationRejectsWritableTargetAndDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX install permissions are exercised on Unix")
	}
	fixture := buildFixture(t, "v0.1.0", oldCommit, "")
	for _, test := range []struct {
		name          string
		targetMode    os.FileMode
		directoryMode os.FileMode
	}{
		{name: "target", targetMode: 0777, directoryMode: 0700},
		{name: "directory", targetMode: 0755, directoryMode: 0777},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := fixtureTargetPath(directory)
			copyFixture(t, fixture, target)
			if err := os.Chmod(target, test.targetMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, test.directoryMode); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := prepareInstallation(target, filepath.Join(directory, ".pgbase-update")); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("writable %s error=%v", test.name, err)
			}
		})
	}
}

func TestRollbackRejectsJournalForAnotherTarget(t *testing.T) {
	directory := t.TempDir()
	secureFixtureDirectory(directory)
	first, second := filepath.Join(directory, "pgbase-a"), filepath.Join(directory, "pgbase-b")
	if runtime.GOOS == "windows" {
		first += ".exe"
		second += ".exe"
	}
	fixture := buildFixture(t, "v0.1.0", oldCommit, "")
	copyFixture(t, fixture, first)
	copyFixture(t, fixture, second)
	state := filepath.Join(directory, ".pgbase-update")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	digest, _, err := fileDigestAndMode(first)
	if err != nil {
		t.Fatal(err)
	}
	entry := journal{SchemaVersion: 1, Phase: "replacement_started", Target: filepath.Base(first), Candidate: ".candidate", Backup: "backup-test",
		Old: Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, New: Build{Version: "v0.2.0", Commit: newCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		OldSHA256: digest, NewSHA256: strings.Repeat("d", 64), ManifestSHA256: strings.Repeat("a", 64), SignatureSHA256: strings.Repeat("b", 64), TrustFingerprint: strings.Repeat("c", 64)}
	if err = writeJournal(state, entry); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(context.Background(), second, state, nil); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("foreign journal error=%v", err)
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TLS port: %v", err)
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
	linkerFlags := "-X main.version=" + version + " -X main.commit=" + commit + " -X main.goos=" + runtime.GOOS + " -X main.goarch=" + runtime.GOARCH + " -X main.failName=" + failName
	if version != "v0.1.0" {
		linkerFlags += " -X main.schemaMin=40 -X main.schemaMax=40"
	}
	arguments := []string{"build", "-trimpath", "-ldflags", linkerFlags, "-o", output, "./internal/selfupdate/testdata/fixture"}
	command := exec.Command("go", arguments...)
	command.Dir = repositoryRoot(t)
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, outputBytes)
	}
	return output
}

func fixtureTargetPath(directory string) string {
	secureFixtureDirectory(directory)
	name := "pgbase"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(directory, name)
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

func TestHTTPSNegativeResponsesAndArtifactCleanup(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	server := startTLSServer(t, func(base string) http.Handler {
		manifest := fixtureManifest("v0.2.0", newCommit, base+"/artifact", 3, strings.Repeat("a", 64))
		raw, _ := json.Marshal(manifest)
		signature := sign(private, raw)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/manifest.json":
				_, _ = w.Write(raw)
			case "/manifest.json.ed25519":
				_, _ = w.Write(signature)
			case "/artifact":
				_, _ = w.Write([]byte("bad"))
			case "/missing":
				w.WriteHeader(http.StatusNotFound)
			case "/redirect":
				http.Redirect(w, r, "/redirect", http.StatusFound)
			}
		})
	})
	if _, err = Fetch(context.Background(), FetchOptions{Source: server.URL + "/missing", TrustKey: public, Current: current, HTTPClient: server.Client()}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("non-2xx=%v", err)
	}
	if _, err = Fetch(context.Background(), FetchOptions{Source: server.URL + "/redirect", TrustKey: public, Current: current, HTTPClient: server.Client()}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("redirect=%v", err)
	}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "candidate")
	if _, err = fetched.DownloadArtifact(context.Background(), destination); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("hash=%v", err)
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("hash mismatch left stage=%v", err)
	}
	if _, err = Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current}); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("untrusted TLS=%v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func TestCopyBoundedPropagatesNoSpace(t *testing.T) {
	if _, _, err := copyBounded(context.Background(), failingWriter{}, strings.NewReader("artifact"), 8); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("ENOSPC=%v", err)
	}
}

func TestArtifactShortContentLengthAndBodyDeadlineCleanup(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/short" {
			w.Header().Set("Content-Length", "5")
			_, _ = w.Write([]byte("bad"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	url, _ := url.Parse(server.URL + "/short")
	fetched := Fetched{Candidate: Candidate{Artifact: Artifact{URL: url.String(), Size: 5, SHA256: strings.Repeat("a", 64)}}, remote: &remoteSource{origin: originOf(url), client: server.Client()}}
	destination := filepath.Join(t.TempDir(), "short")
	if _, err := fetched.DownloadArtifact(context.Background(), destination); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("short=%v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("short cleanup=%v", err)
	}
	blocked, _ := url.Parse(server.URL + "/blocked")
	if _, err := readRemote(context.Background(), &http.Client{Transport: server.Client().Transport, Timeout: 20 * time.Millisecond}, blocked, 16); !errors.Is(err, ErrDownloadInterrupted) {
		t.Fatalf("deadline=%v", err)
	}
}

func TestApplyPermissionFailurePreservesOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
	newBinary := buildFixture(t, "v0.2.0", newCommit, "")
	directory := t.TempDir()
	target := fixtureTargetPath(directory)
	copyFixture(t, oldBinary, target)
	state := filepath.Join(directory, ".pgbase-update")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(newBinary)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixtureManifest("v0.2.0", newCommit, "file://"+newBinary, int64(len(artifact)), checksum(artifact))
	raw, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(state, "manifest.json")
	signaturePath := manifestPath + ".ed25519"
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(signaturePath, sign(private, raw), 0600); err != nil {
		t.Fatal(err)
	}
	current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	fetched, err := Fetch(context.Background(), FetchOptions{Source: manifestPath, SignaturePath: signaturePath, TrustKey: public, Current: current})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
	if _, err = Apply(context.Background(), ApplyOptions{Target: target, StateDir: state, Fetched: fetched, TrustKey: public, Current: current}); err == nil {
		t.Fatal("permission apply succeeded")
	}
	if got, probeErr := ProbeBinary(context.Background(), target); probeErr != nil || !sameBuild(got, current) {
		t.Fatalf("original=%+v err=%v", got, probeErr)
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
