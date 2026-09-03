package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowJobs reads the block structure GitHub Actions workflows actually use
// here: two-space job keys under a top-level `jobs:` mapping. A YAML library
// would add a production dependency for one assertion surface, so the reader
// stays deliberately narrow and fails loudly on anything it cannot classify.
func workflowJobs(t *testing.T, name string) map[string][]string {
	t.Helper()
	lines := workflowLines(t, name)
	jobs := make(map[string][]string)
	current := ""
	inJobs := false
	for _, line := range lines {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			inJobs = strings.HasPrefix(line, "jobs:")
			current = ""
			continue
		}
		if !inJobs {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if indent == 2 && strings.HasSuffix(trimmed, ":") {
			current = strings.TrimSuffix(trimmed, ":")
			jobs[current] = nil
			continue
		}
		if current != "" {
			jobs[current] = append(jobs[current], trimmed)
		}
	}
	if len(jobs) == 0 {
		t.Fatalf("%s declares no jobs", name)
	}
	return jobs
}

func workflowLines(t *testing.T, name string) []string {
	t.Helper()
	return strings.Split(workflowSource(t, name), "\n")
}

func workflowSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func jobField(t *testing.T, jobs map[string][]string, job, field string) string {
	t.Helper()
	body, ok := jobs[job]
	if !ok {
		t.Fatalf("job %q is missing", job)
	}
	for _, line := range body {
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":"))
		}
	}
	return ""
}

func TestReleaseWorkflowGatesEveryPromotionBehindPreflightAndOneCandidate(t *testing.T) {
	jobs := workflowJobs(t, "release-image.yml")
	// Nothing may build, push, or promote before the tag/version preflight, so
	// an unsupported tag costs zero registry writes.
	for job, wantNeeds := range map[string]string{
		"candidate": "preflight",
		"gate":      "candidate",
		"promotion": "[preflight, candidate, gate]",
	} {
		if got := jobField(t, jobs, job, "needs"); got != wantNeeds {
			t.Errorf("release-image.yml job %q needs = %q, want %q", job, got, wantNeeds)
		}
	}
	source := workflowSource(t, "release-image.yml")
	for _, contract := range []string{
		"scripts/support/release-tag-policy.sh",
		"scripts/support/validate-release-candidate.sh",
		"scripts/support/promote-release-image.sh",
		"release-promotion-digests",
	} {
		if !strings.Contains(source, contract) {
			t.Errorf("release-image.yml no longer uses %s", contract)
		}
	}
	if strings.Count(source, "docker buildx build") != 1 {
		t.Error("release-image.yml must build exactly one candidate image")
	}
}

func TestReleasePromotionSerializesAndLabelsEachMovingChannel(t *testing.T) {
	promotion := strings.Join(workflowJobs(t, "release-image.yml")["promotion"], "\n")
	for _, contract := range []string{
		"concurrency:\ngroup: release-promotion-${{ needs.preflight.outputs.channel }}\ncancel-in-progress: false\nqueue: max",
		"CHANNEL_VERSION_OVERRIDE: ${{ vars.PGBASE_RELEASE_CHANNEL_BOOTSTRAP_VERSION }}",
		`"$image" "$GITHUB_REF_NAME" "$CANDIDATE_IMAGE" "$EXPECTED_VERSION" "$CHANNEL_VERSION_OVERRIDE"`,
	} {
		if !strings.Contains(promotion, contract) {
			t.Errorf("release promotion is missing %q", contract)
		}
	}

	dockerfileBytes, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deployments", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(dockerfileBytes)
	runtimeStage := dockerfile[strings.LastIndex(dockerfile, "\nFROM ")+1:]
	for _, contract := range []string{
		"ARG VERSION=dev",
		`org.opencontainers.image.version="${VERSION}"`,
	} {
		if !strings.Contains(runtimeStage, contract) {
			t.Errorf("release runtime image is missing %q", contract)
		}
	}
}

func TestReleaseCandidateVerifiesItsOwnProvenanceAndSBOM(t *testing.T) {
	source := workflowSource(t, "release-image.yml")
	// The verification lives in the candidate job so every gate and the
	// promotion depend on it without adding a sixth release gate.
	candidate := strings.Join(workflowJobs(t, "release-image.yml")["candidate"], "\n")
	if !strings.Contains(candidate, "gh attestation verify") {
		t.Error("the release candidate does not verify its published attestations")
	}
	for _, predicate := range []string{"https://slsa.dev/provenance/v1", "https://spdx.dev/Document/v2.3"} {
		if !strings.Contains(source, predicate) {
			t.Errorf("release-image.yml does not verify the %s attestation", predicate)
		}
	}
	if !strings.Contains(source, "release-attestation-verification") {
		t.Error("release-image.yml keeps no attestation verification evidence")
	}
}

// A historical phase is deployable only after the current rollout harness has
// exercised the exact immutable bundle that supplies its raw binary. Building
// another byte-equivalent-looking bundle in the packaging job would leave the
// published digest disconnected from the A0/M/A/B/C and schema rollout proof.
func TestReleasePhaseCandidatePassesCompositeOracleBeforePackagingTestedBinary(t *testing.T) {
	candidate := strings.Join(workflowJobs(t, "release-phase-image.yml")["candidate"], "\n")
	const (
		membershipBuildCommand = "scripts/support/build-release-phase-artifacts.sh record-membership-v7"
		schemaBuildCommand     = "scripts/support/build-release-phase-artifacts.sh schema-compiler-v9"
		oracleCommand          = "go test -race -tags='integration releaseartifacts' ./tests/release -count=1"
		digestCheck            = `test "sha256:$(sha256sum "$artifact_directory/$artifact_path" | awk '{print $1}')" = "$binary_digest"`
		packageBinary          = `install -m 0755 "$artifact_directory/$artifact_path" "$packaging_context/pgbase"`
		imageBuildCommand      = "docker buildx build"
	)
	contracts := []string{
		"RELEASE_PHASE_ARTIFACT_DIRECTORY: ${{ runner.temp }}/release-phase-artifacts",
		membershipBuildCommand,
		schemaBuildCommand,
		"PGBASE_MEMBERSHIP_RELEASE_ARTIFACT_DIRECTORY: ${{ runner.temp }}/release-phase-artifacts",
		"PGBASE_SCHEMA_COMPILER_RELEASE_ARTIFACT_DIRECTORY: ${{ runner.temp }}/release-phase-artifacts",
		oracleCommand,
		`artifact_directory="$RELEASE_PHASE_ARTIFACT_DIRECTORY"`,
		digestCheck,
		packageBinary,
		imageBuildCommand,
	}
	for _, contract := range contracts {
		if !strings.Contains(candidate, contract) {
			t.Errorf("release-phase-image.yml candidate does not enforce %q", contract)
		}
	}

	membershipBuild := strings.Index(candidate, membershipBuildCommand)
	schemaBuild := strings.Index(candidate, schemaBuildCommand)
	oracle := strings.Index(candidate, oracleCommand)
	digestVerification := strings.Index(candidate, digestCheck)
	packaging := strings.Index(candidate, packageBinary)
	imageBuild := strings.Index(candidate, imageBuildCommand)
	if membershipBuild < 0 || schemaBuild < 0 || oracle < 0 || digestVerification < 0 || packaging < 0 || imageBuild < 0 {
		return
	}
	if membershipBuild > oracle || schemaBuild > oracle || oracle > digestVerification || digestVerification > packaging || packaging > imageBuild {
		t.Errorf("release phase candidate order must be both immutable builds -> composite oracle -> digest check -> raw binary packaging -> image build")
	}
	if count := strings.Count(candidate, imageBuildCommand); count != 1 {
		t.Errorf("release phase candidate builds %d images, want exactly one", count)
	}
}

// The candidate is a single-platform image on purpose: an index digest would
// break the promotion primitive's --prefer-index=false retag. The workflow and
// the deployment documentation have to state the same policy.
func TestSupportedPlatformPolicyMatchesTheDocumentation(t *testing.T) {
	const platform = "linux/amd64"
	source := workflowSource(t, "release-image.yml")
	if !strings.Contains(source, "--platform "+platform) {
		t.Errorf("release-image.yml does not build the candidate for exactly %s", platform)
	}
	for _, unsupported := range []string{"linux/arm64", "linux/arm/v7", "linux/386"} {
		if strings.Contains(source, unsupported) {
			t.Errorf("release-image.yml builds the unsupported platform %s", unsupported)
		}
	}
	deployment, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deployment), platform) {
		t.Errorf("docs/deployment.md does not state the %s platform policy", platform)
	}
}

func TestReleaseGateKeepsTheFiveDigestPinnedGates(t *testing.T) {
	jobs := workflowJobs(t, "release-gate.yml")
	for _, job := range []string{"backend", "frontend", "e2e", "security", "backup_restore"} {
		if _, ok := jobs[job]; !ok {
			t.Errorf("release-gate.yml lost the %q gate", job)
		}
		if needs := jobField(t, jobs, job, "needs"); needs != "contract" {
			t.Errorf("release-gate.yml job %q needs = %q, want contract", job, needs)
		}
	}
	// The candidate contract check plus exactly the five release gates.
	if len(jobs) != 6 {
		t.Errorf("release-gate.yml jobs = %v, want the contract check and five gates", sortedJobNames(jobs))
	}
	for _, job := range []string{"e2e", "security", "backup_restore"} {
		body := strings.Join(jobs[job], "\n")
		for _, input := range []string{"candidate_image:", "expected_version:"} {
			if !strings.Contains(body, input) {
				t.Errorf("release-gate.yml job %q does not forward %s", job, input)
			}
		}
	}
}

func TestImageDependentWorkflowsRefuseAHalfConfiguredCandidate(t *testing.T) {
	for _, workflow := range []string{"e2e.yml", "security.yml", "backup-restore.yml"} {
		source := workflowSource(t, workflow)
		if !strings.Contains(source, "scripts/support/validate-release-candidate.sh") {
			t.Errorf("%s does not validate the candidate reference", workflow)
		}
		if !strings.Contains(source, "candidate_image") || !strings.Contains(source, "expected_version") {
			t.Errorf("%s does not accept the digest-pinned release inputs", workflow)
		}
	}
}

func TestRestoreDrillWorkflowWatchesItsOwnTestSources(t *testing.T) {
	source := workflowSource(t, "backup-restore.yml")
	if count := strings.Count(source, `"tests/restore/**"`); count != 2 {
		t.Errorf("backup-restore.yml push/pull_request path filters cover tests/restore/** %d times, want 2", count)
	}
	if !strings.Contains(source, "go test -tags=restore ./tests/restore") {
		t.Error("backup-restore.yml no longer drives the RestoreDrill through its Go entry point")
	}
	// The PostgreSQL matrix moved into the drill, where it is asserted by
	// tests/restore.TestDefaultPostgresMatrixCoversEverySupportedMajor; the
	// workflow must not reintroduce a single-version service container.
	if strings.Contains(source, "services:") {
		t.Error("backup-restore.yml pins a service database instead of letting the drill cover every major")
	}
}

func TestFrontendGateRejectsUntrackedGeneratedOutput(t *testing.T) {
	source := workflowSource(t, "frontend.yml")
	if !strings.Contains(source, "--untracked-files=all") {
		t.Error("frontend.yml dist gate cannot observe untracked build output")
	}
	if !strings.Contains(source, "rm -rf dist") {
		t.Error("frontend.yml dist gate builds over the committed output instead of a clean directory")
	}
}

// --ignore-scripts is the approved install policy everywhere the frontend is
// installed, and Playwright's browser download stays an explicit command.
func TestEveryFrontendInstallSkipsLifecycleScripts(t *testing.T) {
	sources := map[string]string{
		"frontend.yml": workflowSource(t, "frontend.yml"),
		"e2e.yml":      workflowSource(t, "e2e.yml"),
		"security.yml": workflowSource(t, "security.yml"),
	}
	dockerfile, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deployments", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	sources["deployments/Dockerfile"] = string(dockerfile)
	for name, source := range sources {
		installs := strings.Count(source, "npm ci")
		guarded := strings.Count(source, "npm ci --ignore-scripts")
		if installs == 0 {
			t.Errorf("%s no longer installs the frontend", name)
		}
		if installs != guarded {
			t.Errorf("%s runs %d npm ci commands but only %d skip lifecycle scripts", name, installs, guarded)
		}
	}
	if !strings.Contains(sources["e2e.yml"], "npx playwright install --with-deps chromium") {
		t.Error("e2e.yml no longer installs the Playwright browser as an explicit command")
	}
}

// workflowSteps splits a workflow's `steps:` entries. Steps are the unit the
// release contract is written in — a source build is allowed only while a step
// is switched off in release mode — and the surrounding job structure carries
// no `if:` that could stand in for the step's own.
func workflowSteps(t *testing.T, name string) [][]string {
	t.Helper()
	var steps [][]string
	var current []string
	inStep := false
	for _, line := range workflowLines(t, name) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasPrefix(trimmed, "- name:") && indent == 6 {
			if inStep {
				steps = append(steps, current)
			}
			current = []string{trimmed}
			inStep = true
			continue
		}
		if !inStep {
			continue
		}
		if indent < 8 {
			steps = append(steps, current)
			current = nil
			inStep = false
			continue
		}
		current = append(current, trimmed)
	}
	if inStep {
		steps = append(steps, current)
	}
	if len(steps) == 0 {
		t.Fatalf("%s declares no steps", name)
	}
	return steps
}

// Release mode judges the published candidate or nothing. A gate that builds
// its own image scans, or runs, an artifact the release never publishes, and
// the two would agree right up until the moment they did not. Both workflows
// already carry the condition; without this test nothing fails when it is
// dropped, which is the class of unguarded guard the 2026-09-01 audit named.
func TestImageGatesNeverBuildTheirOwnArtifactInReleaseMode(t *testing.T) {
	for _, workflow := range []string{"security.yml", "e2e.yml"} {
		built := 0
		for _, step := range workflowSteps(t, workflow) {
			body := strings.Join(step, "\n")
			if !strings.Contains(body, "docker build") && !strings.Contains(body, "docker compose -f deployments/docker-compose.yml build") {
				continue
			}
			built++
			if !strings.Contains(body, "release == 'false'") {
				t.Errorf("%s step %q builds an image without excluding release mode", workflow, step[0])
			}
		}
		if built != 1 {
			t.Errorf("%s has %d image build steps, want exactly the local-CI one", workflow, built)
		}
	}
}

func sortedJobNames(jobs map[string][]string) []string {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	return names
}
