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

func TestReleaseWorkflowPinsBuilderAndInjectsVersion(t *testing.T) {
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
	if !strings.Contains(workflow, `tag="${GITHUB_REF_NAME}"`) || !strings.Contains(buildCommand, `--build-arg "VERSION=$tag"`) {
		t.Fatal("release workflow does not inject the release tag into the binary version")
	}

	dockerfilePath := filepath.Join("..", "..", "deployments", "Dockerfile")
	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read release Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), `-X github.com/webisnoteasy/PgBase/pgbase.Version=${VERSION}`) {
		t.Fatal("release Dockerfile does not link the VERSION build argument into pgbase.Version")
	}
}

func TestReleaseWorkflowRequiresCompleteReusableVerificationBeforePublishing(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-image.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, marker := range []string{
		"backend:\n    uses: ./.github/workflows/backend.yml",
		"frontend:\n    uses: ./.github/workflows/frontend.yml",
		"e2e:\n    uses: ./.github/workflows/e2e.yml",
		"security:\n    uses: ./.github/workflows/security.yml",
		"backup_restore:\n    uses: ./.github/workflows/backup-restore.yml",
		"needs: [backend, frontend, e2e, security, backup_restore]",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("release workflow is missing verification gate %q", marker)
		}
	}

	for _, name := range []string{"backend.yml", "frontend.yml", "e2e.yml", "security.yml", "backup-restore.yml"} {
		path := filepath.Join("..", "..", ".github", "workflows", name)
		calledWorkflow, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(calledWorkflow), "workflow_call:") {
			t.Fatalf("%s is not reusable by the release gate", name)
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

func TestBackupRestoreDrillCoversAppRelationDataDiagnosticsAndToolCompatibility(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "backup-restore.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, marker := range []string{
		"create table app.restore_records",
		"restore-record-sentinel",
		"select count(*) from app.restore_records",
		"restore_people",
		"restore_articles",
		"pgbase_meta.field_relations",
		"relations.cardinality = 'multiple'",
		"string_agg(people.name, ',' order by edges.position)",
		"go run ./cmd/pgbase doctor --json",
		"pg_restore --version",
		"server_version_num",
	} {
		if !strings.Contains(strings.ToLower(workflow), strings.ToLower(marker)) {
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
