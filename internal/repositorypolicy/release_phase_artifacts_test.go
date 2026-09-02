package repositorypolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendGateBuildsAndRunsReleasePhaseArtifactsFromFullHistory(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	job := strings.Index(workflow, "  release-phase-artifacts:\n")
	if job < 0 {
		t.Fatal("backend gate has no release-phase-artifacts job")
	}
	section := workflow[job:]
	for _, marker := range []string{
		"fetch-depth: 0",
		"PGBASE_TEST_ADMIN_URL:",
		"scripts/support/build-release-phase-artifacts.sh record-membership-v7",
		"scripts/support/build-release-phase-artifacts.sh schema-compiler-v9",
		"PGBASE_MEMBERSHIP_RELEASE_ARTIFACT_DIRECTORY:",
		"PGBASE_SCHEMA_COMPILER_RELEASE_ARTIFACT_DIRECTORY:",
		"go test -race -tags='integration releaseartifacts' ./tests/release -count=1",
		`tar --create --gzip`,
		`--file "$RUNNER_TEMP/release-phase-artifacts.tar.gz"`,
		"path: ${{ runner.temp }}/release-phase-artifacts.tar.gz",
		"name: release-phase-artifacts",
	} {
		if !strings.Contains(section, marker) {
			t.Errorf("release artifact gate is missing %q", marker)
		}
	}
}
