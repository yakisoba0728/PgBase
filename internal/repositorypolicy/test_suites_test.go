package repositorypolicy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationSuiteRejectsMissingAdminURLBeforeInvokingGo(t *testing.T) {
	root := filepath.Join("..", "..")
	marker := filepath.Join(t.TempDir(), "go-invoked")
	fakeBin := t.TempDir()
	fakeGo := filepath.Join(fakeBin, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\n: > \"$PGBASE_FAKE_GO_MARKER\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "test-go-integration.sh"))
	command.Env = append(withoutEnvironment(os.Environ(), "PGBASE_TEST_ADMIN_URL"),
		"PATH="+fakeBin,
		"PGBASE_FAKE_GO_MARKER="+marker,
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatal("integration suite succeeded without PGBASE_TEST_ADMIN_URL")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("integration suite invoked go before its environment preflight: %v", err)
	}
	if !strings.Contains(stderr.String(), "PGBASE_TEST_ADMIN_URL") {
		t.Fatalf("preflight diagnostic = %q", stderr.String())
	}
}

func TestBackendWorkflowRunsUnitOnceAndLiveTestsAcrossSupportedPostgres(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, marker := range []string{
		"name: Go unit",
		"scripts/test-go-unit.sh -race -count=1",
		"name: PostgreSQL integration / ${{ matrix.postgres.version }}",
		`version: "16.13"`,
		`version: "17.9"`,
		`version: "18.3"`,
		"scripts/test-go-integration.sh -race -count=1",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("backend workflow is missing %q", marker)
		}
	}
}

func withoutEnvironment(environment []string, name string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment))
	for _, value := range environment {
		if !strings.HasPrefix(value, prefix) {
			result = append(result, value)
		}
	}
	return result
}

// The onboarding specs need an instance that has no administrator yet, so they
// can never share a backend with the authenticated ones. Playwright skips the
// authenticated project entirely when the second origin is absent, which is a
// silent loss of coverage rather than a failure, so the wiring is pinned here.
func TestBrowserWorkflowRunsBothProjectsAgainstIsolatedBackends(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)

	for _, required := range []string{
		"PGBASE_ONBOARDING_BROWSER_BASE_URL: http://127.0.0.1:18090",
		"PGBASE_AUTHENTICATED_BROWSER_BASE_URL: http://127.0.0.1:18091",
		"PGBASE_AUTH_PROJECT_NAME=",
		"Create the authenticated backend's administrator",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("e2e.yml is missing %q", required)
		}
	}

	// Two Compose projects, or the second instance reuses the first one's
	// volumes and arrives with an administrator already set up.
	if strings.Count(workflow, "COMPOSE_PROJECT_NAME") < 2 {
		t.Error("e2e.yml does not give the authenticated backend its own Compose project")
	}
	// Whatever the run does, both projects have to be torn down.
	teardown := workflow[strings.LastIndex(workflow, "down --volumes"):]
	if strings.Count(workflow, "down --volumes") < 2 {
		t.Errorf("e2e.yml leaves a Compose project running: %q", teardown[:min(len(teardown), 120)])
	}
}
