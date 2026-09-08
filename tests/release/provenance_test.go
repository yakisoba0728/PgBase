package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhaseProvenanceSeparatesHistoricalSourceFromPackagingWorkflow(t *testing.T) {
	source := strings.Repeat("a", 40)
	workflow := strings.Repeat("b", 40)
	output := filepath.Join(t.TempDir(), "provenance.json")
	cmd := exec.Command(filepath.Join(repositoryRoot(t), "scripts/support/create-release-provenance.sh"), output, "v0.0.7-rc.0", source)
	cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=yakisoba0728/PgBase", "GITHUB_SHA="+workflow,
		"GITHUB_REF=refs/heads/main", "GITHUB_WORKFLOW_REF=yakisoba0728/PgBase/.github/workflows/release-phase-image.yml@refs/heads/main",
		"GITHUB_RUN_ID=123", "GITHUB_RUN_ATTEMPT=2", "GITHUB_SERVER_URL=https://github.com")
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate provenance: %v\n%s", err, result)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		BuildDefinition struct {
			ResolvedDependencies []struct {
				URI    string
				Digest map[string]string
			}
			InternalParameters struct {
				Workflow struct{ Commit string }
			}
		}
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	dependencies := provenance.BuildDefinition.ResolvedDependencies
	if len(dependencies) != 1 || dependencies[0].URI != "git+https://github.com/yakisoba0728/PgBase@"+source || dependencies[0].Digest["gitCommit"] != source {
		t.Fatalf("dependency must identify the immutable historical source: %+v", dependencies)
	}
	if got := provenance.BuildDefinition.InternalParameters.Workflow.Commit; got != workflow {
		t.Fatalf("packaging workflow commit = %q, want %q", got, workflow)
	}
}
