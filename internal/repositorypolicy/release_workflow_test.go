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
