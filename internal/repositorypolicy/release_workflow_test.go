package repositorypolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
		"needs: [backend, frontend, e2e, security]",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("release workflow is missing verification gate %q", marker)
		}
	}

	for _, name := range []string{"backend.yml", "frontend.yml", "e2e.yml", "security.yml"} {
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
