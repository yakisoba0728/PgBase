//go:build releaseartifacts

package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type builtReleaseArtifactManifest struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Rollout       string                 `json:"rollout"`
	Artifacts     []builtReleaseArtifact `json:"artifacts"`
}

type builtReleaseArtifact struct {
	Phase   string `json:"phase"`
	Commit  string `json:"commit"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Build   struct {
		BuiltAt   string `json:"builtAt"`
		SchemaMin string `json:"schemaMin"`
		SchemaMax string `json:"schemaMax"`
	} `json:"build"`
	Toolchain struct {
		GoVersion string `json:"goVersion"`
		GOOS      string `json:"goOS"`
		GOARCH    string `json:"goArch"`
	} `json:"toolchain"`
}

func TestMembershipRolloutBuildProducesFiveDistinctExecutables(t *testing.T) {
	output := membershipArtifactsForProcessTest(t)
	digests := make(map[[sha256.Size]byte]string)
	actualDigests := make(map[string]string)
	for _, phase := range []string{"a0", "a", "b", "c", "operator"} {
		path := filepath.Join(output, "pgbase-record-membership-v7-"+phase)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s artifact: %v", phase, err)
		}
		if len(contents) == 0 {
			t.Fatalf("%s artifact is empty", phase)
		}
		digest := sha256.Sum256(contents)
		if previous, duplicate := digests[digest]; duplicate {
			t.Fatalf("%s and %s artifacts are byte-identical", previous, phase)
		}
		digests[digest] = phase
		actualDigests[phase] = "sha256:" + hex.EncodeToString(digest[:])
	}
	metadata, err := os.ReadFile(filepath.Join(output, "record-membership-v7-artifacts.json"))
	if err != nil {
		t.Fatalf("read artifact metadata: %v", err)
	}
	var built builtReleaseArtifactManifest
	if err := json.Unmarshal(metadata, &built); err != nil {
		t.Fatalf("decode artifact metadata: %v", err)
	}
	if built.SchemaVersion != 1 || built.Rollout != "record-membership-v7" || len(built.Artifacts) != 5 {
		t.Fatalf("artifact metadata = %#v", built)
	}
	manifest := readReleasePhaseManifest(t)
	var declared releaseRollout
	for _, rollout := range manifest.Rollouts {
		if rollout.Name == "record-membership-v7" {
			declared = rollout
			break
		}
	}
	if declared.Operator == nil {
		t.Fatal("record membership rollout has no pinned operator")
	}
	declaredArtifacts := append([]releaseArtifact(nil), declared.Artifacts...)
	declaredArtifacts = append(declaredArtifacts, releaseArtifact{
		Phase: "Operator", Commit: declared.Operator.Commit, Version: declared.Operator.Version,
	})
	for index, phase := range []string{"A0", "A", "B", "C", "Operator"} {
		entry := built.Artifacts[index]
		lowerPhase := strings.ToLower(phase)
		wantPath := "pgbase-record-membership-v7-" + lowerPhase
		declaration := declaredArtifacts[index]
		if entry.Phase != phase || entry.Commit != declaration.Commit || entry.Version != declaration.Version ||
			entry.Path != wantPath || entry.Digest != actualDigests[lowerPhase] ||
			entry.Toolchain.GoVersion != "go1.26.10" || entry.Toolchain.GOOS == "" || entry.Toolchain.GOARCH == "" {
			t.Fatalf("artifact metadata entry %d = %#v, want phase=%s commit=%s version=%s path=%s digest=%s with pinned toolchain",
				index, entry, phase, declaration.Commit, declaration.Version, wantPath, actualDigests[lowerPhase])
		}
	}
}

func TestReleaseArtifactBuilderFailsClosedWithoutPinnedGitObjects(t *testing.T) {
	sourceScript, err := os.ReadFile(filepath.Join("..", "..", "scripts", "support", "build-release-phase-artifacts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const missingCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	manifest := []byte(`{"schemaVersion":1,"rollouts":[{"name":"missing-source","migration":1,"artifacts":[{"phase":"P0","commit":"` + missingCommit + `","version":"v0.0.1-alpha.0"}]}]}`)

	tests := []struct {
		name    string
		gitInit bool
		want    string
	}{
		{name: "source archive", want: "require the repository's Git objects"},
		{name: "missing object", gitInit: true, want: "source commit is unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			scriptDirectory := filepath.Join(repository, "scripts", "support")
			if err := os.MkdirAll(scriptDirectory, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repository, "deployments"), 0o755); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(scriptDirectory, "build-release-phase-artifacts.sh")
			if err := os.WriteFile(script, sourceScript, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repository, "deployments", "release-phases.json"), manifest, 0o644); err != nil {
				t.Fatal(err)
			}
			if test.gitInit {
				command := exec.Command("git", "init", "--quiet", repository)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("initialize source repository: %v\n%s", err, output)
				}
			}
			command := exec.Command(script, "missing-source", filepath.Join(repository, "output"))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("missing source build error = %v, output = %q, want %q", err, output, test.want)
			}
		})
	}
}

func TestReleaseArtifactBuilderRejectsAnUnsupportedManifestSchema(t *testing.T) {
	sourceScript, err := os.ReadFile(filepath.Join("..", "..", "scripts", "support", "build-release-phase-artifacts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	scriptDirectory := filepath.Join(repository, "scripts", "support")
	if err := os.MkdirAll(scriptDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "deployments"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDirectory, "build-release-phase-artifacts.sh")
	if err := os.WriteFile(script, sourceScript, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2,"rollouts":[{"name":"unsupported","migration":1,"artifacts":[{"phase":"P0","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","version":"v0.0.1-alpha.0"}]}]}`)
	if err := os.WriteFile(filepath.Join(repository, "deployments", "release-phases.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--quiet", repository).CombinedOutput(); err != nil {
		t.Fatalf("initialize source repository: %v\n%s", err, output)
	}
	command := exec.Command(script, "unsupported", filepath.Join(repository, "output"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "unsupported release phase manifest schema") {
		t.Fatalf("unsupported manifest error = %v output=%q", err, output)
	}
}

func buildReleaseArtifacts(t *testing.T, rollout string) string {
	t.Helper()
	output := t.TempDir()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		filepath.Join(repositoryRoot, "scripts", "support", "build-release-phase-artifacts.sh"),
		rollout,
		output,
	)
	command.Dir = repositoryRoot
	if built, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s rollout artifacts: %v\n%s", rollout, err, built)
	}
	return output
}

func membershipArtifactsForProcessTest(t *testing.T) string {
	t.Helper()
	if prebuilt := os.Getenv("PGBASE_MEMBERSHIP_RELEASE_ARTIFACT_DIRECTORY"); prebuilt != "" {
		return prebuilt
	}
	return buildReleaseArtifacts(t, "record-membership-v7")
}
