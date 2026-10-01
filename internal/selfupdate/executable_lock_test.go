//go:build !windows

package selfupdate

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const serveLockHelperEnv = "PGBASE_N9_SERVE_LOCK_HELPER"
const serveLockTargetEnv = "PGBASE_N9_SERVE_LOCK_TARGET"

func secureFixtureDirectory(string) {}

// TestServeLockHelper runs only in a separately spawned test process. Its
// lifetime models serve: keep the executable FD locked until stdin closes.
func TestServeLockHelper(t *testing.T) {
	if os.Getenv(serveLockHelperEnv) != "1" {
		return
	}
	lock, err := AcquireServeLock(os.Getenv(serveLockTargetEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdin.Read(make([]byte, 1))
}

func TestServeLifetimeLockRejectsUpdateAcrossProcesses(t *testing.T) {
	target := fixtureTargetPath(t.TempDir())
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)

	command := exec.Command(os.Args[0], "-test.run=^TestServeLockHelper$")
	command.Env = append(os.Environ(), serveLockHelperEnv+"=1", serveLockTargetEnv+"="+target)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Wait()
	})

	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() && scanner.Text() == "ready" {
			ready <- nil
			return
		}
		ready <- scanner.Err()
	}()
	select {
	case err = <-ready:
		if err != nil {
			t.Fatalf("serve helper readiness: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve helper did not acquire executable lock")
	}

	// A spelling with an extra separator resolves to the same executable inode.
	alias := filepath.Dir(target) + string(filepath.Separator) + string(filepath.Separator) + filepath.Base(target)
	if _, err = acquireUpdateExecutableLock(alias); !errors.Is(err, ErrReplaceBusy) {
		t.Fatalf("update lock while serve runs: %v", err)
	}
	if _, err = Apply(context.Background(), ApplyOptions{
		Target: target, StateDir: filepath.Join(filepath.Dir(target), ".pgbase-update"),
		Fetched:  Fetched{ManifestRaw: []byte("manifest"), SignatureRaw: []byte("signature"), Candidate: Candidate{Artifact: Artifact{Size: 1}}},
		TrustKey: make(ed25519.PublicKey, ed25519.PublicKeySize),
		Current:  Build{Version: "v0.1.0", Commit: oldCommit},
	}); !errors.Is(err, ErrReplaceBusy) {
		t.Fatalf("apply while serve runs: %v", err)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatalf("serve helper exit: %v", err)
	}
	if lock, err := acquireUpdateExecutableLock(target); err != nil {
		t.Fatalf("update lock after serve exits: %v", err)
	} else if err = closeExecutableLock(lock); err != nil {
		t.Fatal(err)
	}
}
