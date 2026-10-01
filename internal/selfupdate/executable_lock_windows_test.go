//go:build windows

package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

const windowsHelperTestRequest = "PGBASE_WINDOWS_HELPER_TEST_REQUEST"

func secureFixtureDirectory(path string) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		panic(err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		panic(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		panic(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		panic(err)
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{user.User.Sid, system, administrators} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS,
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee:     windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)},
		})
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		panic(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		panic(err)
	}
}

func TestWindowsHelperProcess(t *testing.T) {
	encoded := os.Getenv(windowsHelperTestRequest)
	if encoded == "" {
		return
	}
	if err := RunWindowsHelperArgument(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestExecutableLockAllowsProbe(t *testing.T) {
	target := fixtureTargetPath(t.TempDir())
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)
	lock, err := acquireUpdateExecutableLock(target)
	if err != nil {
		t.Fatal(err)
	}
	defer closeExecutableLock(lock)
	if _, err = ProbeBinary(context.Background(), target); err != nil {
		t.Fatalf("probe locked executable: %v", err)
	}
}

func TestWindowsReplaceFilePublishesNewAndExistingTargets(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	for index, value := range []string{"first", "second"} {
		source := filepath.Join(directory, "source")
		if err := os.WriteFile(source, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if err := replaceFile(source, target); err != nil {
			t.Fatalf("replace %d: %v", index, err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != value {
			t.Fatalf("replace %d got=%q err=%v", index, got, err)
		}
	}
}

func TestWindowsCoordinationLockSurvivesExecutableReplacement(t *testing.T) {
	directory := t.TempDir()
	secureFixtureDirectory(directory)
	target := filepath.Join(directory, "pgbase.exe")
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)
	updateLock, err := acquireUpdateExecutableLock(target)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement.exe")
	copyFixture(t, buildFixture(t, "v0.2.0", newCommit, ""), replacement)
	if err = replaceFile(replacement, target); err != nil {
		t.Fatal(err)
	}
	if _, err = AcquireServeLock(target); !errors.Is(err, ErrReplaceBusy) {
		t.Fatalf("serve acquired lock during replacement window: %v", err)
	}
	if err = closeExecutableLock(updateLock); err != nil {
		t.Fatal(err)
	}
	serveLock, err := AcquireServeLock(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = serveLock.Close()
}

func TestWindowsRejectsBroadlyWritableInstallationACL(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "pgbase.exe")
	copyFixture(t, buildFixture(t, "v0.1.0", oldCommit, ""), target)
	if _, _, err := validateInstallation(target, filepath.Join(directory, ".pgbase-update"), false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("broad ACL validation error=%v", err)
	}
}

func TestWindowsNativeUpdateLifecycle(t *testing.T) {
	for _, test := range []struct {
		name       string
		failHealth bool
	}{
		{name: "old_to_new_and_explicit_rollback"},
		{name: "failed_canonical_health_rolls_back", failHealth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldBinary := buildFixture(t, "v0.1.0", oldCommit, "")
			install := t.TempDir()
			target := fixtureTargetPath(install)
			copyFixture(t, oldBinary, target)
			failName := ""
			if test.failHealth {
				failName = filepath.Base(target)
			}
			newBinary := buildFixture(t, "v0.2.0", newCommit, failName)
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
				raw, _ := json.Marshal(manifest)
				signature := sign(private, raw)
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/manifest.json":
						_, _ = w.Write(raw)
					case "/manifest.json.ed25519":
						_, _ = w.Write(signature)
					case "/artifact":
						_, _ = w.Write(newBytes)
					default:
						http.NotFound(w, r)
					}
				})
			})
			current := Build{Version: "v0.1.0", Commit: oldCommit, GOOS: "windows", GOARCH: "amd64"}
			fetched, err := Fetch(context.Background(), FetchOptions{Source: server.URL + "/manifest.json", TrustKey: public, Current: current, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			var helper *exec.Cmd
			launch := func(request WindowsHelperRequest) error {
				request.ParentPID = ^uint32(0)
				raw, marshalErr := json.Marshal(request)
				if marshalErr != nil {
					return marshalErr
				}
				helper = exec.Command(os.Args[0], "-test.run=^TestWindowsHelperProcess$")
				helper.Env = append(os.Environ(), windowsHelperTestRequest+"="+base64.RawURLEncoding.EncodeToString(raw))
				return helper.Start()
			}
			state := filepath.Join(install, ".pgbase-update")
			status, err := Apply(context.Background(), ApplyOptions{Target: target, StateDir: state, Fetched: fetched, TrustKey: public, Current: current, WindowsLaunch: launch})
			if err != nil || status.Phase != "replacement_started" {
				t.Fatalf("restart handoff status=%+v err=%v", status, err)
			}
			helperErr := helper.Wait()
			final, readErr := ReadStatus(target, state)
			if test.failHealth {
				if helperErr == nil || readErr != nil || final.Phase != "rolled_back" {
					t.Fatalf("failed health helper=%v status=%+v read=%v", helperErr, final, readErr)
				}
			} else {
				if helperErr != nil || readErr != nil || final.Phase != "committed" {
					t.Fatalf("helper=%v status=%+v read=%v", helperErr, final, readErr)
				}
				if _, err = Rollback(context.Background(), target, state, nil); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := ProbeBinary(context.Background(), target)
			if err != nil || restored.Version != "v0.1.0" {
				t.Fatalf("restored=%+v err=%v", restored, err)
			}

			lock, err := AcquireServeLock(target)
			if err != nil {
				t.Fatal(err)
			}
			_, busyErr := Apply(context.Background(), ApplyOptions{Target: target, StateDir: filepath.Join(install, ".second-update"), Fetched: Fetched{ManifestRaw: []byte("x"), SignatureRaw: []byte("x"), Candidate: Candidate{Artifact: Artifact{Size: 1}}}, TrustKey: make([]byte, 32), Current: current})
			_ = lock.Close()
			if !errors.Is(busyErr, ErrReplaceBusy) {
				t.Fatalf("serve/update lock error=%v", busyErr)
			}
		})
	}
}
