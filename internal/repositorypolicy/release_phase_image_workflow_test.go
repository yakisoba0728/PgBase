package repositorypolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePhaseImageWorkflowSelectsOneImmutableArtifactAndGatesItsDigest(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-phase-image.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)

	for _, marker := range []string{
		"workflow_dispatch:",
		"rollout:\n        description: Exact rollout name from deployments/release-phases.json",
		"phase:\n        description: Exact phase name within the selected rollout",
		"[.rollouts[] | select(.name == $rollout) | .artifacts[] | select(.phase == $phase)]",
		"release phase must resolve exactly once",
		`test("^[0-9a-f]{40}$")`,
		".[0].version",
		`scripts/support/release-tag-policy.sh "$expected_version" "$expected_version"`,
		`git merge-base --is-ancestor "$source_commit" "$GITHUB_SHA"`,
		"name: Check out the gate and full artifact history",
		"fetch-depth: 0",
		`scripts/support/build-release-phase-artifacts.sh record-membership-v7`,
		`scripts/support/build-release-phase-artifacts.sh schema-compiler-v9`,
		`go test -race -tags='integration releaseartifacts' ./tests/release -count=1`,
		`artifact_directory="$RELEASE_PHASE_ARTIFACT_DIRECTORY"`,
		`'[.artifacts[] | select(.phase == $phase)] | select(length == 1) | .[0]'`,
		`test "$(jq --raw-output '.commit' <<< "$selected")" = "$SOURCE_COMMIT"`,
		`test "sha256:$(sha256sum "$artifact_directory/$artifact_path" | awk '{print $1}')" = "$binary_digest"`,
		"--file deployments/release-phase.Dockerfile",
		`--build-arg "SOURCE_COMMIT=$SOURCE_COMMIT"`,
		`--build-arg "VERSION=$EXPECTED_VERSION"`,
		"--provenance=mode=max",
		"--sbom=true",
		"candidate_image=$candidate_image",
		"actions/attest-build-provenance@",
		"actions/attest-sbom@",
		"actions/attest@",
		"artifact-metadata: write",
		"commit: $sourceCommit",
		".source.repository == $sourceRepository",
		"packaging: {workflowCommit: $workflowCommit}",
		"binaryDigest: $binaryDigest",
		"predicate-type: https://github.com/webisnoteasy/PgBase/attestations/release-phase/v1",
		"provenance https://slsa.dev/provenance/v1",
		"sbom https://spdx.dev/Document/v2.3",
		"source https://github.com/webisnoteasy/PgBase/attestations/release-phase/v1",
		"jq --exit-status 'length > 0'",
		".source.commit == $sourceCommit",
		".packaging.workflowCommit == $workflowCommit",
		".version == $version",
		".binaryDigest == $binaryDigest",
		"uses: ./.github/workflows/release-gate.yml",
		"candidate_image: ${{ needs.candidate.outputs.candidate_image }}",
		"expected_version: ${{ needs.resolve.outputs.expected_version }}",
		"source_commit: ${{ needs.resolve.outputs.source_commit }}",
	} {
		if !strings.Contains(workflow, marker) {
			t.Errorf("release phase image workflow is missing %q", marker)
		}
	}
	if strings.Count(workflow, "docker buildx build") != 1 {
		t.Fatalf("release phase workflow builds the production candidate %d times, want exactly once", strings.Count(workflow, "docker buildx build"))
	}
	if strings.Contains(workflow, "promotion:") || strings.Contains(workflow, "promote-release-image.sh") || strings.Contains(workflow, `--tag "$image:latest"`) {
		t.Fatal("release phase verification can promote a candidate")
	}
}

func TestReleasePhasePackagingOnlyCopiesThePinnedBinaryIntoTheHardenedRuntime(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "deployments", "release-phase.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(contents)
	for _, marker := range []string{
		"FROM gcr.io/distroless/static-debian12:nonroot@sha256:",
		"ARG SOURCE_COMMIT",
		"ARG VERSION",
		`org.opencontainers.image.revision="$SOURCE_COMMIT"`,
		`org.opencontainers.image.version="$VERSION"`,
		"COPY --chown=nonroot:nonroot pgbase /pgbase",
		"USER nonroot:nonroot",
		`ENTRYPOINT ["/pgbase"]`,
	} {
		if !strings.Contains(dockerfile, marker) {
			t.Errorf("release phase packaging is missing %q", marker)
		}
	}
	for _, forbidden := range []string{"npm ", "go build", "COPY .", "curl ", "wget ", "ADD "} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("release phase packaging reruns an untrusted source build via %q", forbidden)
		}
	}
}

func TestReleaseGateBindsTheCandidateSourceWithoutRewindingNewerGateHarnesses(t *testing.T) {
	gatePath := filepath.Join("..", "..", ".github", "workflows", "release-gate.yml")
	contents, err := os.ReadFile(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	gate := string(contents)
	for _, marker := range []string{
		"ref: ${{ inputs.source_commit || github.sha }}",
		"source commit must be a full lowercase commit object ID",
		`test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"`,
	} {
		if !strings.Contains(gate, marker) {
			t.Errorf("release gate source contract is missing %q", marker)
		}
	}
	jobs := []string{"backend", "frontend", "e2e", "security", "backup_restore"}
	for index, job := range jobs {
		start := strings.Index(gate, "  "+job+":\n")
		if start < 0 {
			t.Fatalf("release gate has no %s job", job)
		}
		end := len(gate)
		if index+1 < len(jobs) {
			end = strings.Index(gate, "  "+jobs[index+1]+":\n")
		}
		section := gate[start:end]
		for _, binding := range []string{
			"candidate_image: ${{ inputs.candidate_image }}",
			"expected_version: ${{ inputs.expected_version }}",
			"source_commit: ${{ inputs.source_commit }}",
		} {
			if !strings.Contains(section, binding) {
				t.Errorf("release gate %s job is missing %q", job, binding)
			}
		}
	}

	const sourceCheckout = "ref: ${{ inputs.source_commit || github.sha }}"
	for _, name := range []string{"backend.yml", "frontend.yml", "e2e.yml", "security.yml", "backup-restore.yml"} {
		path := filepath.Join("..", "..", ".github", "workflows", name)
		calledBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		called := string(calledBytes)
		if !strings.Contains(called, "source_commit:\n        description:") {
			t.Errorf("%s does not declare the optional source commit", name)
		}
	}

	backendBytes, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	backend := string(backendBytes)
	releaseArtifacts := strings.Index(backend, "  release-phase-artifacts:\n")
	if releaseArtifacts < 0 || !strings.Contains(backend[releaseArtifacts:], "if: inputs.source_commit == ''") {
		t.Fatal("historical source gates can recursively run the release-phase artifact matrix")
	}
	if strings.Count(backend, sourceCheckout) != 3 || strings.Count(backend, "ref: ${{ github.sha }}") != 2 {
		t.Fatal("backend must test the phase source while keeping observability and artifact harnesses at the gate revision")
	}

	for _, name := range []string{"frontend.yml", "e2e.yml"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(contents), sourceCheckout) != strings.Count(string(contents), "uses: actions/checkout@") {
			t.Errorf("%s does not run its source-compatible tests at the selected phase commit", name)
		}
	}

	securityBytes, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	security := string(securityBytes)
	for _, marker := range []string{
		sourceCheckout,
		"name: Check out current release security policy",
		"ref: ${{ github.sha }}",
		"path: .release-gate-policy",
		".release-gate-policy/deployments/security/trivy-allowlist.yaml",
	} {
		if !strings.Contains(security, marker) {
			t.Errorf("security gate is missing phase-safe policy binding %q", marker)
		}
	}

	restoreBytes, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backup-restore.yml"))
	if err != nil {
		t.Fatal(err)
	}
	restore := string(restoreBytes)
	if !strings.Contains(restore, "ref: ${{ github.sha }}") || strings.Contains(restore, sourceCheckout) {
		t.Fatal("restore must run the current cross-version harness against the selected digest")
	}
}
