package repositorypolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestReleaseWorkflowPinsBuilderAndInjectsPreflightVersionIntoOneCandidate(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-image.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)

	createCommand := shellCommand(t, workflow, "docker buildx create")
	pinnedBuildKit := regexp.MustCompile(`moby/buildkit:buildx-stable-1@sha256:[0-9a-f]{64}`)
	if !strings.Contains(createCommand, "--driver docker-container") || !pinnedBuildKit.MatchString(createCommand) {
		t.Fatal("release workflow does not pin the BuildKit image by digest")
	}
	buildCommand := shellCommand(t, workflow, "docker buildx build")
	if strings.Count(workflow, "docker buildx build") != 1 ||
		!strings.Contains(buildCommand, `--build-arg "VERSION=$release_version"`) ||
		!strings.Contains(buildCommand, `--build-arg "SOURCE_COMMIT=$GITHUB_SHA"`) ||
		!strings.Contains(buildCommand, `--build-arg "BUILT_AT=$built_at"`) {
		t.Fatal("release workflow does not inject complete build metadata into exactly one candidate build")
	}
	if strings.Contains(buildCommand, `--tag "$image:$tag"`) || strings.Contains(buildCommand, `--tag "$image:latest"`) {
		t.Fatal("candidate build mutates an exact release tag or moving channel before verification")
	}

	dockerfilePath := filepath.Join("..", "..", "deployments", "Dockerfile")
	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read release Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), `-X github.com/webisnoteasy/PgBase/pgbase.Version=${VERSION}`) {
		t.Fatal("release Dockerfile does not link the VERSION build argument into pgbase.Version")
	}
	for _, metadataLink := range []string{
		`internal/buildinfo.Commit=${SOURCE_COMMIT}`,
		`internal/buildinfo.BuiltAt=${BUILT_AT}`,
		`internal/buildinfo.SchemaMin=$schema_version`,
		`internal/buildinfo.SchemaMax=$schema_version`,
		`/out/pgbase version --json`,
	} {
		if !strings.Contains(string(dockerfile), metadataLink) {
			t.Fatalf("release Dockerfile is missing build metadata contract %q", metadataLink)
		}
	}
	runtimeStage := string(dockerfile)[strings.LastIndex(string(dockerfile), "\nFROM ")+1:]
	if !strings.Contains(runtimeStage, "ARG VERSION=dev") ||
		!strings.Contains(runtimeStage, `org.opencontainers.image.version="${VERSION}"`) {
		t.Fatal("release runtime image does not carry the canonical version OCI label")
	}
}

func TestNativeArtifactBuildersInjectCompleteBuildMetadata(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "scripts", "build-local-artifacts.sh"),
		filepath.Join("..", "..", "scripts", "support", "build-release-phase-artifacts.sh"),
	}
	markers := []string{
		"internal/buildinfo.Commit=",
		"internal/buildinfo.BuiltAt=",
		"internal/buildinfo.SchemaMin=",
		"internal/buildinfo.SchemaMax=",
		"version --json",
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(contents), marker) {
				t.Errorf("%s is missing build metadata contract %q", filepath.Base(path), marker)
			}
		}
	}
}

func TestReleaseWorkflowUsesCandidateFirstDigestPinnedGateBeforePromotion(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-image.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, marker := range []string{
		"group: release-${{ github.ref }}\n  cancel-in-progress: false",
		"preflight:",
		"candidate:\n    needs: preflight",
		"candidate_image: ${{ steps.candidate.outputs.candidate_image }}",
		"expected_version: ${{ needs.preflight.outputs.expected_version }}",
		"gate:\n    needs: candidate\n    uses: ./.github/workflows/release-gate.yml",
		"candidate_image: ${{ needs.candidate.outputs.candidate_image }}",
		"expected_version: ${{ needs.candidate.outputs.expected_version }}",
		"promotion:\n    needs: [preflight, candidate, gate]",
		"group: release-promotion-${{ needs.preflight.outputs.channel }}\n      cancel-in-progress: false\n      queue: max",
		"CHANNEL_VERSION_OVERRIDE: ${{ vars.PGBASE_RELEASE_CHANNEL_BOOTSTRAP_VERSION }}",
		"scripts/support/promote-release-image.sh",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("release workflow is missing candidate-first contract %q", marker)
		}
	}
	preflight := strings.Index(workflow, "preflight:")
	build := strings.Index(workflow, "docker buildx build")
	promotion := strings.Index(workflow, "scripts/support/promote-release-image.sh")
	if preflight < 0 || build < preflight || promotion < build {
		t.Fatal("release operations are not ordered preflight, candidate build, promotion")
	}
	if strings.Contains(workflow, "docker push") || strings.Contains(workflow[promotion:], "docker build") {
		t.Fatal("promotion rebuilds or directly pushes an image")
	}

	gatePath := filepath.Join("..", "..", ".github", "workflows", "release-gate.yml")
	gateBytes, err := os.ReadFile(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	gate := string(gateBytes)
	for _, marker := range []string{
		"candidate_image:\n        description:",
		"candidate_image:\n        description: Digest-pinned candidate image\n        required: true",
		"expected_version:\n        description: Canonical release version\n        required: true",
		"backend:\n    needs: contract\n    uses: ./.github/workflows/backend.yml",
		"frontend:\n    needs: contract\n    uses: ./.github/workflows/frontend.yml",
		"e2e:\n    needs: contract\n    uses: ./.github/workflows/e2e.yml",
		"security:\n    needs: contract\n    uses: ./.github/workflows/security.yml",
		"backup_restore:\n    needs: contract\n    uses: ./.github/workflows/backup-restore.yml",
	} {
		if !strings.Contains(gate, marker) {
			t.Errorf("reusable release gate is missing %q", marker)
		}
	}

	for _, name := range []string{"backend.yml", "frontend.yml", "e2e.yml", "security.yml", "backup-restore.yml", "release-gate.yml"} {
		path := filepath.Join("..", "..", ".github", "workflows", name)
		calledWorkflow, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(calledWorkflow), "workflow_call:") {
			t.Fatalf("%s is not reusable by the release gate", name)
		}
	}

	for _, consumer := range []struct {
		name    string
		markers []string
	}{
		{name: "e2e.yml", markers: []string{
			"PGBASE_E2E_CANDIDATE_IMAGE", "PGBASE_E2E_EXPECTED_VERSION",
			"scripts/support/validate-release-candidate.sh", "--no-build",
		}},
		{name: "security.yml", markers: []string{
			"PGBASE_SECURITY_CANDIDATE_IMAGE", "scripts/support/validate-release-candidate.sh",
			"image-ref: ${{ steps.image.outputs.image_ref }}",
		}},
		{name: "backup-restore.yml", markers: []string{
			"PGBASE_RESTORE_CANDIDATE_IMAGE", "PGBASE_RESTORE_EXPECTED_VERSION",
			"scripts/support/validate-release-candidate.sh",
			"go test -tags=restore ./tests/restore",
		}},
	} {
		contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", consumer.name))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range consumer.markers {
			if !strings.Contains(string(contents), marker) {
				t.Errorf("%s does not enforce release candidate contract %q", consumer.name, marker)
			}
		}
	}

	e2eSource, err := os.ReadFile(filepath.Join("..", "..", "tests", "e2e", "e2e_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`os.Getenv("PGBASE_E2E_CANDIDATE_IMAGE")`,
		`os.Getenv("PGBASE_E2E_EXPECTED_VERSION")`,
		`if suite.release {`,
		`startupArguments = append(startupArguments, "--no-build")`,
	} {
		if !strings.Contains(string(e2eSource), marker) {
			t.Errorf("black-box E2E release mode is missing %q", marker)
		}
	}

	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "deployments", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dockerfile), "RUN go test ./...") {
		t.Fatal("release Dockerfile treats database-skipping tests as an artifact-build gate")
	}
	for _, staleBuildOnlyInput := range []string{".github/workflows/release-image.yml", "deployments/Dockerfile ./deployments/Dockerfile", "ui/admin/handler_test.go"} {
		if strings.Contains(string(dockerfile), staleBuildOnlyInput) {
			t.Fatalf("release Dockerfile copies test-only input %q", staleBuildOnlyInput)
		}
	}
}

func TestPlaywrightReleaseGateDoesNotMaskFailuresWithRetries(t *testing.T) {
	config, err := os.ReadFile(filepath.Join("..", "..", "ui", "admin", "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*retries:\s*0,\s*$`).Match(config) {
		t.Fatal("Playwright release coverage relies on a retry instead of deterministic preconditions")
	}

	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(workflow), "--retries") {
		t.Fatal("E2E workflow overrides the zero-retry Playwright policy")
	}
}

func TestSecurityWorkflowLimitsTheGatedSARIFReportToHighSeverityFindings(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	start := strings.Index(workflow, "      - name: Record image findings as SARIF\n")
	if start < 0 {
		t.Fatal("security workflow has no gated SARIF report step")
	}
	end := strings.Index(workflow[start+1:], "\n      - name:")
	if end < 0 {
		t.Fatal("security workflow has no step after the gated SARIF report")
	}
	step := workflow[start : start+1+end]
	for _, marker := range []string{
		"severity: HIGH,CRITICAL",
		"limit-severities-for-sarif: true",
	} {
		if !strings.Contains(step, marker) {
			t.Fatalf("gated SARIF report is missing %q", marker)
		}
	}
}

func TestRoutineSecurityChecksDeferImageWorkToExplicitOrReleaseRuns(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	selector := strings.Index(workflow, "      - name: Select the image under test\n")
	if selector < 0 {
		t.Fatal("security workflow has no image selector")
	}
	selectorEnd := strings.Index(workflow[selector+1:], "\n      - name:")
	if selectorEnd < 0 {
		t.Fatal("security workflow has no step after the image selector")
	}
	selectorStep := workflow[selector : selector+1+selectorEnd]
	for _, marker := range []string{
		"inputs.candidate_image != ''",
		"github.event_name == 'workflow_dispatch'",
		"github.event_name == 'schedule'",
	} {
		if !strings.Contains(selectorStep, marker) {
			t.Errorf("security image selector is missing %q", marker)
		}
	}
	for _, stepName := range []string{
		"Generate SPDX SBOM",
		"Record image findings as SARIF",
		"Record every severity as SARIF",
		"Scan production image",
	} {
		start := strings.Index(workflow, "      - name: "+stepName+"\n")
		if start < 0 {
			t.Errorf("security workflow has no %q step", stepName)
			continue
		}
		end := strings.Index(workflow[start+1:], "\n      - name:")
		if end < 0 {
			end = len(workflow) - start - 1
		}
		if !strings.Contains(workflow[start:start+1+end], "steps.image.outputs.image_ref != ''") {
			t.Errorf("security step %q can run without a selected image", stepName)
		}
	}
}

func TestBackupRestoreDrillCoversAppRelationDataDiagnosticsAndToolCompatibility(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "backup-restore.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	// The workflow now owns only scheduling and candidate wiring; the drill
	// procedure itself lives in tests/restore, where it is executable rather
	// than embedded in shell.
	for _, marker := range []string{
		"workflow_call:",
		"workflow_dispatch:",
		"schedule:",
		"go test -tags=restore ./tests/restore",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("backup restore workflow is missing %q", marker)
		}
	}
	for _, costlyTrigger := range []string{"\n  push:", "\n  pull_request:"} {
		if strings.Contains(workflow, costlyTrigger) {
			t.Fatalf("backup restore workflow unexpectedly runs on %s", strings.TrimSpace(costlyTrigger))
		}
	}
	drill := readDrillSources(t)
	for _, marker := range []string{
		"restore_people",
		"restore_articles",
		"pgbase_meta.field_relations",
		"relations.cardinality = 'multiple'",
		"string_agg(people.name, ',' order by edges.position)",
		"pgbase_meta.record_revisions",
		"pgbase_meta.record_audit_events",
		"pgbase_meta.record_outbox",
		"pgbase_meta.record_idempotency",
		"pgbase_meta.schema_operations",
		"Idempotency-Replayed",
		`getHeaders.Get("ETag")`,
		`"doctor", "--json"`,
		`"pg_restore", "--version"`,
		"server_version_num",
	} {
		if !strings.Contains(drill, marker) {
			t.Fatalf("backup restore drill is missing %q", marker)
		}
	}

	documentation, err := os.ReadFile(filepath.Join("..", "..", "docs", "backup-restore.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"pg_restore --version", "server_version_num", "client major"} {
		if !strings.Contains(strings.ToLower(string(documentation)), strings.ToLower(marker)) {
			t.Fatalf("backup restore documentation is missing compatibility guard %q", marker)
		}
	}
	documentationText := string(documentation)
	serviceFile := strings.LastIndex(documentationText, `export PGSERVICEFILE=`)
	versionCheck := strings.LastIndex(documentationText, `server_version_num=`)
	if serviceFile < 0 || versionCheck < 0 || serviceFile > versionCheck {
		t.Fatal("restore target service files are not selected before the server-version guard")
	}
}

// TestBackupDocumentationCoversEverySystemSchema keeps the security policy and
// the backup procedure on one schema list, derived from the schemas the system
// migrations actually create. A document that names fewer schemas than the
// database contains instructs operators to take backups that cannot be
// restored into a working project.
func TestBackupDocumentationCoversEverySystemSchema(t *testing.T) {
	createSchema := regexp.MustCompile(`(?i)create schema (?:if not exists )?([a-z_][a-z0-9_]*)`)
	migrated := make(map[string]struct{})
	for _, migration := range system.Migrations() {
		for _, match := range createSchema.FindAllStringSubmatch(migration.SQL, -1) {
			migrated[match[1]] = struct{}{}
		}
	}
	required := make([]string, 0, len(migrated))
	for schema := range migrated {
		required = append(required, schema)
	}
	sort.Strings(required)
	if len(required) == 0 {
		t.Fatal("system migrations declare no schemas; the backup scope policy cannot be checked")
	}

	backupPath := filepath.Join("..", "..", "docs", "backup-restore.md")
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup documentation: %v", err)
	}
	backupDocumentation := string(backup)

	dumpFlag := regexp.MustCompile(`--schema=([a-z_][a-z0-9_]*)`)
	dumped := make([]string, 0, len(required))
	for _, match := range dumpFlag.FindAllStringSubmatch(backupDocumentation, -1) {
		dumped = append(dumped, match[1])
	}
	sort.Strings(dumped)
	if len(dumped) == 0 {
		t.Fatalf("%s no longer documents a pg_dump command with --schema flags", backupPath)
	}

	introduction, _, _ := strings.Cut(backupDocumentation, ". ")
	summarized := backtickedIdentifiers(introduction)
	if len(summarized) == 0 {
		t.Fatalf("%s no longer opens by naming the schemas a valid backup contains", backupPath)
	}

	securityPath := filepath.Join("..", "..", "SECURITY.md")
	security, err := os.ReadFile(securityPath)
	if err != nil {
		t.Fatalf("read security policy: %v", err)
	}
	securityPolicy := string(security)
	scope := regexp.MustCompile(`back up [^()\n]*\(([^)\n]*)\)`).FindStringSubmatch(securityPolicy)
	if scope == nil {
		t.Fatalf("%s no longer states the backup scope as a parenthesized schema list", securityPath)
	}
	policed := backtickedIdentifiers(scope[1])

	for _, listed := range []struct {
		source  string
		what    string
		schemas []string
	}{
		{source: backupPath, what: "pg_dump command", schemas: dumped},
		{source: backupPath, what: "summary of a valid backup", schemas: summarized},
		{source: securityPath, what: "deployment responsibility", schemas: policed},
	} {
		if strings.Join(listed.schemas, ",") != strings.Join(required, ",") {
			t.Errorf("%s %s backs up %v; the system migrations create %v, and omitting one schema produces an unrestorable backup",
				listed.source, listed.what, listed.schemas, required)
		}
	}

	// The policy has to send operators to the procedure it summarizes, so read
	// SECURITY.md itself instead of only checking that the target file exists.
	const backupLink = "[Backup and restore](docs/backup-restore.md)"
	if !strings.Contains(securityPolicy, backupLink) {
		t.Errorf("%s no longer links to the backup procedure with %s", securityPath, backupLink)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Errorf("backup procedure linked from %s does not resolve: %v", securityPath, err)
	}
}

func readDrillSources(t *testing.T) string {
	t.Helper()
	sources, err := filepath.Glob(filepath.Join("..", "..", "tests", "restore", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 {
		t.Fatal("the restore drill has no Go sources")
	}
	var combined strings.Builder
	for _, source := range sources {
		contents, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		combined.Write(contents)
	}
	return combined.String()
}

func backtickedIdentifiers(text string) []string {
	matches := regexp.MustCompile("`([a-z_][a-z0-9_]*)`").FindAllStringSubmatch(text, -1)
	identifiers := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, duplicate := seen[match[1]]; duplicate {
			continue
		}
		seen[match[1]] = struct{}{}
		identifiers = append(identifiers, match[1])
	}
	sort.Strings(identifiers)
	return identifiers
}

func shellCommand(t *testing.T, input, start string) string {
	t.Helper()
	lines := strings.Split(input, "\n")
	for index := range lines {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, start) {
			continue
		}
		command := line
		for strings.HasSuffix(command, `\`) {
			index++
			if index >= len(lines) {
				t.Fatalf("release workflow command %q has an unterminated continuation", start)
			}
			command += "\n" + strings.TrimSpace(lines[index])
		}
		return command
	}
	t.Fatalf("release workflow is missing shell command %q", start)
	return ""
}
