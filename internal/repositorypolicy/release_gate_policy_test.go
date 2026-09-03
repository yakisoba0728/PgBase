package repositorypolicy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	releaseImage       = "ghcr.io/acme/pgbase"
	candidateDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	differentDigest    = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oldChannelDigest   = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	releaseCandidate   = releaseImage + "@" + candidateDigest
	releaseExpectedVer = "v1.2.3"
)

func TestReleaseTagPolicyImplementsCanonicalFiveRowMatrix(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		channel string
	}{
		{name: "stable", tag: "v1.2.3", channel: "latest"},
		{name: "alpha", tag: "v1.2.3-alpha.4", channel: "alpha"},
		{name: "beta", tag: "v1.2.3-beta.5", channel: "beta"},
		{name: "release candidate", tag: "v1.2.3-rc.6", channel: "rc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := runReleaseTagPolicy(test.tag, test.tag)
			if err != nil {
				t.Fatalf("canonical tag rejected: %v\nstderr: %s", err, stderr)
			}
			want := "expected_version=" + test.tag + "\nchannel=" + test.channel + "\n"
			if stdout != want {
				t.Fatalf("policy output = %q, want %q", stdout, want)
			}
		})
	}

	invalid := []string{
		"v1.2",
		"v01.2.3",
		"v1.2.3-alpha.01",
		"v1.2.3+build.1",
		"v1.2.3-preview.1",
		"1.2.3",
	}
	for _, tag := range invalid {
		t.Run("reject "+tag, func(t *testing.T) {
			_, stderr, err := runReleaseTagPolicy(tag, tag)
			if err == nil {
				t.Fatalf("unsupported release tag %q was accepted", tag)
			}
			if !strings.Contains(stderr, "unsupported release tag") {
				t.Fatalf("diagnostic for %q = %q", tag, stderr)
			}
		})
	}

	if _, stderr, err := runReleaseTagPolicy("v1.2.3", "v1.2.4"); err == nil {
		t.Fatal("release tag/version mismatch was accepted")
	} else if !strings.Contains(stderr, "does not match expected version") {
		t.Fatalf("version mismatch diagnostic = %q", stderr)
	}
}

func TestReleasePromotionHandlesAbsentSameAndDifferentExactTagsWithoutRebuild(t *testing.T) {
	tests := []struct {
		name          string
		exactDigest   string
		wantSuccess   bool
		wantMutations int
	}{
		{name: "absent exact tag is created", wantSuccess: true, wantMutations: 2},
		{name: "same exact digest is an idempotent no-op", exactDigest: candidateDigest, wantSuccess: true, wantMutations: 1},
		{name: "different exact digest rejects every mutation", exactDigest: differentDigest, wantSuccess: false, wantMutations: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			registry.setVersioned(releaseImage+":latest", oldChannelDigest, "v1.2.2")
			if test.exactDigest != "" {
				if test.exactDigest == candidateDigest {
					registry.setVersioned(releaseImage+":"+releaseExpectedVer, test.exactDigest, releaseExpectedVer)
				} else {
					registry.set(releaseImage+":"+releaseExpectedVer, test.exactDigest)
				}
			}

			stdout, stderr, err := registry.promote(releaseExpectedVer, releaseExpectedVer)
			if test.wantSuccess && err != nil {
				t.Fatalf("promotion failed: %v\nstderr: %s", err, stderr)
			}
			if !test.wantSuccess && err == nil {
				t.Fatal("promotion unexpectedly succeeded")
			}
			if got := registry.mutations(); len(got) != test.wantMutations {
				t.Fatalf("registry mutations = %v, want %d", got, test.wantMutations)
			}

			if !test.wantSuccess {
				operations := registry.operations()
				if len(operations) != 1 || !strings.Contains(operations[0], releaseImage+":"+releaseExpectedVer) {
					t.Fatalf("different digest operations = %v, want only the exact-tag read", operations)
				}
				if got := registry.get(releaseImage + ":latest"); got != oldChannelDigest {
					t.Fatalf("rejected promotion moved latest to %q", got)
				}
				if !strings.Contains(stderr, "immutable exact tag") {
					t.Fatalf("different-digest diagnostic = %q", stderr)
				}
				return
			}
			if got := registry.get(releaseImage + ":" + releaseExpectedVer); got != candidateDigest {
				t.Fatalf("exact tag digest = %q, want %q", got, candidateDigest)
			}
			if got := registry.get(releaseImage + ":latest"); got != candidateDigest {
				t.Fatalf("latest digest = %q, want %q", got, candidateDigest)
			}
			for _, marker := range []string{
				"exact_before=", "channel_before=", "exact_after=" + candidateDigest,
				"channel_after=" + candidateDigest,
			} {
				if !strings.Contains(stdout, marker) {
					t.Fatalf("promotion audit = %q, missing %q", stdout, marker)
				}
			}
		})
	}
}

func TestReleasePromotionTreatsRegistryReadErrorsAsFailuresNotAbsence(t *testing.T) {
	registry := newFakeRegistry(t)
	registry.inspectError = "unauthorized: authentication required"
	if _, stderr, err := registry.promote(releaseExpectedVer, releaseExpectedVer); err == nil {
		t.Fatal("promotion treated an authorization error as an absent exact tag")
	} else if !strings.Contains(stderr, "could not read registry digest") {
		t.Fatalf("registry error diagnostic = %q", stderr)
	}
	if got := registry.mutations(); len(got) != 0 {
		t.Fatalf("registry read failure caused mutations: %v", got)
	}
}

func TestReleaseCandidateValidationAcceptsOnlyDigestAndCanonicalVersionPairs(t *testing.T) {
	root := filepath.Join("..", "..")
	script := filepath.Join(root, "scripts", "support", "validate-release-candidate.sh")
	tests := []struct {
		name      string
		candidate string
		version   string
		valid     bool
	}{
		{name: "digest pinned", candidate: releaseCandidate, version: releaseExpectedVer, valid: true},
		{name: "missing candidate", candidate: "", version: releaseExpectedVer},
		{name: "mutable tag", candidate: releaseImage + ":candidate", version: releaseExpectedVer},
		{name: "tag plus digest", candidate: releaseImage + ":candidate@" + candidateDigest, version: releaseExpectedVer},
		{name: "uppercase digest", candidate: releaseImage + "@sha256:" + strings.Repeat("A", 64), version: releaseExpectedVer},
		{name: "unsupported version", candidate: releaseCandidate, version: "v1.2.3-preview.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", script, test.candidate, test.version)
			err := command.Run()
			if test.valid && err != nil {
				t.Fatalf("valid candidate rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestReleasePromotionMapsPrereleaseChannelsAndRejectsInvalidInputsBeforeRegistryAccess(t *testing.T) {
	for _, test := range []struct {
		tag     string
		channel string
	}{
		{tag: "v1.2.3-alpha.1", channel: "alpha"},
		{tag: "v1.2.3-beta.2", channel: "beta"},
		{tag: "v1.2.3-rc.3", channel: "rc"},
	} {
		t.Run(test.channel, func(t *testing.T) {
			registry := newFakeRegistry(t)
			if _, stderr, err := registry.promote(test.tag, test.tag); err != nil {
				t.Fatalf("promotion failed: %v\nstderr: %s", err, stderr)
			}
			if got := registry.get(releaseImage + ":" + test.channel); got != candidateDigest {
				t.Fatalf("%s digest = %q, want %q", test.channel, got, candidateDigest)
			}
			for _, untouched := range []string{"latest", "alpha", "beta", "rc"} {
				if untouched == test.channel {
					continue
				}
				if got := registry.get(releaseImage + ":" + untouched); got != "" {
					t.Fatalf("promotion unexpectedly moved %s to %q", untouched, got)
				}
			}
		})
	}

	registry := newFakeRegistry(t)
	if _, _, err := registry.promote("v1.2.3+build.1", "v1.2.3+build.1"); err == nil {
		t.Fatal("promotion accepted build metadata")
	}
	if got := registry.operations(); len(got) != 0 {
		t.Fatalf("invalid promotion accessed the registry: %v", got)
	}

	registry = newFakeRegistry(t)
	if _, _, err := registry.promote("v1.2.3", "v1.2.4"); err == nil {
		t.Fatal("promotion accepted a tag/version mismatch")
	}
	if got := registry.operations(); len(got) != 0 {
		t.Fatalf("tag/version mismatch accessed the registry: %v", got)
	}

	registry = newFakeRegistry(t)
	badCandidate := releaseImage + ":candidate"
	if _, _, err := registry.promoteWithCandidate(releaseExpectedVer, releaseExpectedVer, badCandidate); err == nil {
		t.Fatal("promotion accepted a tag-based candidate")
	}
	if got := registry.operations(); len(got) != 0 {
		t.Fatalf("tag-based candidate accessed the registry: %v", got)
	}
}

func runReleaseTagPolicy(tag, expectedVersion string) (string, string, error) {
	root := filepath.Join("..", "..")
	command := exec.Command("bash", filepath.Join(root, "scripts", "support", "release-tag-policy.sh"), tag, expectedVersion)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

type fakeRegistry struct {
	t            *testing.T
	directory    string
	fakeBin      string
	logPath      string
	promotePath  string
	inspectError string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	root := filepath.Join("..", "..")
	directory := t.TempDir()
	fakeBin := t.TempDir()
	logPath := filepath.Join(directory, "operations.log")
	dockerPath := filepath.Join(fakeBin, "docker")
	fakeDocker := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$PGBASE_FAKE_REGISTRY_LOG"
state_file() {
  local ref="$1"
  ref="${ref//\//_}"
  ref="${ref//:/_}"
  ref="${ref//@/_}"
  printf '%s/%s' "$PGBASE_FAKE_REGISTRY_STATE" "$ref"
}
if [[ "$1 $2 $3" == "buildx imagetools inspect" ]]; then
  ref="$4"
  if [[ -n "${PGBASE_FAKE_INSPECT_ERROR:-}" ]]; then
    echo "$PGBASE_FAKE_INSPECT_ERROR" >&2
    exit 1
  fi
  file="$(state_file "$ref")"
  if [[ ! -f "$file" ]]; then
    echo "ERROR: $ref: not found" >&2
    exit 1
  fi
  if [[ "${5:-}" == "--format" && "${6:-}" == *'.Image'* ]]; then
    version_file="$file.version"
    if [[ ! -f "$version_file" ]]; then
      printf '{"config":{"Labels":{}}}\n'
      exit 0
    fi
    printf '{"config":{"Labels":{"org.opencontainers.image.version":"%s"}}}\n' "$(<"$version_file")"
    exit 0
  fi
  printf '{"digest":"%s"}\n' "$(<"$file")"
  exit 0
fi
if [[ "$1 $2 $3" == "buildx imagetools create" ]]; then
  shift 3
  target=''
  source=''
  while (($#)); do
    case "$1" in
      --tag) target="$2"; shift 2 ;;
      --prefer-index=false) shift ;;
      *) source="$1"; shift ;;
    esac
  done
  [[ -n "$target" && "$source" == *@sha256:* ]]
  target_file="$(state_file "$target")"
  source_file="$(state_file "$source")"
  printf '%s' "${source##*@}" > "$target_file"
  if [[ -f "$source_file.version" ]]; then
    cp "$source_file.version" "$target_file.version"
  fi
  printf 'MUTATE %s %s\n' "$target" "$source" >> "$PGBASE_FAKE_REGISTRY_LOG"
  exit 0
fi
echo "unsupported fake docker invocation: $*" >&2
exit 97
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeRegistry{
		t: t, directory: directory, fakeBin: fakeBin, logPath: logPath,
		promotePath: filepath.Join(root, "scripts", "support", "promote-release-image.sh"),
	}
}

func (r *fakeRegistry) promote(tag, expectedVersion string) (string, string, error) {
	return r.promoteWithCandidate(tag, expectedVersion, releaseCandidate)
}

func (r *fakeRegistry) promoteWithCandidate(tag, expectedVersion, candidate string) (string, string, error) {
	r.t.Helper()
	if strings.Contains(candidate, "@") {
		r.setVersioned(candidate, candidate[strings.LastIndex(candidate, "@")+1:], expectedVersion)
	}
	command := exec.Command("bash", r.promotePath, releaseImage, tag, candidate, expectedVersion)
	command.Env = append(os.Environ(),
		"PATH="+r.fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PGBASE_FAKE_REGISTRY_LOG="+r.logPath,
		"PGBASE_FAKE_REGISTRY_STATE="+r.directory,
		"PGBASE_FAKE_INSPECT_ERROR="+r.inspectError,
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func (r *fakeRegistry) set(ref, digest string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.directory, fakeRegistryKey(ref)), []byte(digest), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fakeRegistry) setVersioned(ref, digest, version string) {
	r.t.Helper()
	r.set(ref, digest)
	if err := os.WriteFile(filepath.Join(r.directory, fakeRegistryKey(ref)+".version"), []byte(version), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fakeRegistry) get(ref string) string {
	r.t.Helper()
	contents, err := os.ReadFile(filepath.Join(r.directory, fakeRegistryKey(ref)))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return string(contents)
}

func (r *fakeRegistry) operations() []string {
	r.t.Helper()
	contents, err := os.ReadFile(r.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.FieldsFunc(strings.TrimSpace(string(contents)), func(r rune) bool { return r == '\n' })
}

func (r *fakeRegistry) mutations() []string {
	r.t.Helper()
	var mutations []string
	for _, operation := range r.operations() {
		if strings.HasPrefix(operation, "MUTATE ") {
			mutations = append(mutations, operation)
		}
	}
	return mutations
}

func fakeRegistryKey(ref string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", "@", "_")
	return replacer.Replace(ref)
}

func TestReleasePolicyScriptsAreExecutable(t *testing.T) {
	root := filepath.Join("..", "..", "scripts", "support")
	for _, name := range []string{"release-tag-policy.sh", "promote-release-image.sh", "validate-release-candidate.sh"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %s)", name, fmt.Sprintf("%#o", info.Mode().Perm()))
		}
	}
}
