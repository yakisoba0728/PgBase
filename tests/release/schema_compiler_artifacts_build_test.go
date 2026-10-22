//go:build releaseartifacts

package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaCompilerRolloutBuildProducesDistinctPredecessorPreparationAndFeatureExecutables(t *testing.T) {
	output := schemaCompilerArtifactsForProcessTest(t)
	var digests [3][sha256.Size]byte
	actualDigests := make(map[string]string)
	for index, phase := range []string{"predecessor", "p0", "f"} {
		path := filepath.Join(output, "pgbase-schema-compiler-v9-"+phase)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s artifact: %v", phase, err)
		}
		if len(contents) == 0 {
			t.Fatalf("%s artifact is empty", phase)
		}
		digests[index] = sha256.Sum256(contents)
		actualDigests[phase] = "sha256:" + hex.EncodeToString(digests[index][:])
	}
	for first := range digests {
		for second := first + 1; second < len(digests); second++ {
			if digests[first] == digests[second] {
				t.Fatalf("schema compiler artifacts %d and %d are byte-identical", first, second)
			}
		}
	}
	metadata, err := os.ReadFile(filepath.Join(output, "schema-compiler-v9-artifacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var built builtReleaseArtifactManifest
	if err := json.Unmarshal(metadata, &built); err != nil {
		t.Fatalf("decode schema compiler artifact metadata: %v", err)
	}
	manifest := readReleasePhaseManifest(t)
	var declared releaseRollout
	for _, rollout := range manifest.Rollouts {
		if rollout.Name == "schema-compiler-v9" {
			declared = rollout
			break
		}
	}
	if declared.Predecessor == nil {
		t.Fatal("schema compiler rollout has no pinned predecessor")
	}
	if built.SchemaVersion != 1 || built.Rollout != declared.Name || len(built.Artifacts) != 3 {
		t.Fatalf("schema compiler artifact metadata = %#v", built)
	}
	declaredArtifacts := []releaseArtifact{{
		Phase: "Predecessor", Commit: declared.Predecessor.Commit, Version: declared.Predecessor.Version,
	}}
	declaredArtifacts = append(declaredArtifacts, declared.Artifacts...)
	for index, phase := range []string{"Predecessor", "P0", "F"} {
		entry := built.Artifacts[index]
		declaration := declaredArtifacts[index]
		lowerPhase := []string{"predecessor", "p0", "f"}[index]
		wantPath := "pgbase-schema-compiler-v9-" + lowerPhase
		if entry.Phase != phase || entry.Commit != declaration.Commit || entry.Version != declaration.Version ||
			entry.Path != wantPath || entry.Digest != actualDigests[lowerPhase] ||
			entry.Toolchain.GoVersion != "go1.26.10" || entry.Toolchain.GOOS == "" || entry.Toolchain.GOARCH == "" {
			t.Fatalf("schema compiler metadata entry %d = %#v", index, entry)
		}
	}
}

func schemaCompilerArtifactsForProcessTest(t *testing.T) string {
	t.Helper()
	if prebuilt := os.Getenv("PGBASE_SCHEMA_COMPILER_RELEASE_ARTIFACT_DIRECTORY"); prebuilt != "" {
		return prebuilt
	}
	return buildReleaseArtifacts(t, "schema-compiler-v9")
}
