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

func TestBackendWorkflowKeepsLeanAndReleasePostgresMatrices(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, marker := range []string{
		"name: Go unit",
		"scripts/test-go-unit.sh -race -count=1",
		"name: PostgreSQL integration / ${{ matrix.postgres.version }}",
		"fromJSON((inputs.candidate_image != '' || github.event_name == 'workflow_dispatch')",
		`{"version":"16.13"`,
		`{"version":"17.9"`,
		`{"version":"18.3"`,
		`|| '[{"version":"17.9"`,
		"scripts/test-go-integration.sh -race -count=1",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("backend workflow is missing %q", marker)
		}
	}
}

func TestRepositoryScopedRunnerRequiresAnExactInternalPullRequestOrMainPushSHA(t *testing.T) {
	const guardedRunner = "(((github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && github.event.pull_request.head.sha == vars.PGBASE_CI_TRUSTED_SHA) || (github.event_name == 'push' && github.ref == 'refs/heads/main' && github.sha == vars.PGBASE_CI_TRUSTED_SHA)) && vars.PGBASE_CI_RUNNER) || 'ubuntu-24.04'"
	for name, wantGuards := range map[string]int{"backend.yml": 3, "frontend.yml": 1, "security.yml": 1} {
		raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(raw)
		if got := strings.Count(workflow, guardedRunner); got != wantGuards {
			t.Errorf("%s exact trusted PR-or-main-SHA runner guards = %d, want %d", name, got, wantGuards)
		}
		if !strings.Contains(workflow, "branches: [main]") || !strings.Contains(workflow, "pull_request:") {
			t.Errorf("%s does not separate main pushes from PR validation", name)
		}
	}

	for _, test := range []struct {
		name       string
		event      string
		ref        string
		repository string
		headRepo   string
		eventSHA   string
		headSHA    string
		trustedSHA string
		runner     string
		want       bool
	}{
		{name: "exact internal pull request", event: "pull_request", repository: "owner/repo", headRepo: "owner/repo", headSHA: "trusted", trustedSHA: "trusted", runner: "temporary", want: true},
		{name: "fork pull request", event: "pull_request", repository: "owner/repo", headRepo: "fork/repo", headSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "different pull request SHA", event: "pull_request", repository: "owner/repo", headRepo: "owner/repo", headSHA: "other", trustedSHA: "trusted", runner: "temporary"},
		{name: "exact main push", event: "push", ref: "refs/heads/main", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary", want: true},
		{name: "different main SHA", event: "push", ref: "refs/heads/main", eventSHA: "other", trustedSHA: "trusted", runner: "temporary"},
		{name: "tag push", event: "push", ref: "refs/tags/v0.1.0", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "other branch push", event: "push", ref: "refs/heads/release", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "manual dispatch", event: "workflow_dispatch", ref: "refs/heads/main", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "schedule", event: "schedule", ref: "refs/heads/main", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "reusable release", event: "workflow_call", ref: "refs/heads/main", eventSHA: "trusted", trustedSHA: "trusted", runner: "temporary"},
		{name: "missing runner label", event: "push", ref: "refs/heads/main", eventSHA: "trusted", trustedSHA: "trusted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := temporaryRunnerEligible(test.event, test.ref, test.repository, test.headRepo, test.eventSHA, test.headSHA, test.trustedSHA, test.runner)
			if got != test.want {
				t.Fatalf("temporary runner eligibility = %t, want %t", got, test.want)
			}
		})
	}

	for _, name := range []string{"e2e.yml", "backup-restore.yml"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(raw)
		if strings.Contains(workflow, "vars.PGBASE_CI_RUNNER") {
			t.Errorf("%s routes a release-strength Docker job to the developer runner", name)
		}
		if !strings.Contains(workflow, "workflow_dispatch:") || !strings.Contains(workflow, "workflow_call:") {
			t.Errorf("%s is not available to explicit or release-gate execution", name)
		}
	}
}

func temporaryRunnerEligible(event, ref, repository, headRepo, eventSHA, headSHA, trustedSHA, runner string) bool {
	if trustedSHA == "" || runner == "" {
		return false
	}
	if event == "pull_request" {
		return headRepo == repository && headSHA == trustedSHA
	}
	return event == "push" && ref == "refs/heads/main" && eventSHA == trustedSHA
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

// The terminal adapter is implemented per platform and its cancellation and
// echo-restoration tests carry a darwin build tag, so the Ubuntu unit job does
// not compile them at all. A green CI without this job would say nothing about
// the code path that restores a terminal after an interrupted password prompt.
func TestBackendWorkflowRunsTheDarwinTerminalTestsOnMacOS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)

	if !strings.Contains(workflow, "runs-on: macos-") {
		t.Error("backend.yml has no macOS runner, so the darwin terminal tests never run in CI")
	}

	// Every darwin-tagged test function must be named by the job, so renaming
	// one or letting its build tag drift fails here instead of silently
	// dropping the only coverage it has.
	entries, err := os.ReadDir(filepath.Join("..", "cli"))
	if err != nil {
		t.Fatal(err)
	}
	darwinTests := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join("..", "cli", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), "//go:build darwin") {
			continue
		}
		for _, line := range strings.Split(string(source), "\n") {
			name, found := strings.CutPrefix(line, "func Test")
			if !found {
				continue
			}
			name = "Test" + name[:strings.Index(name, "(")]
			darwinTests++
			if !strings.Contains(workflow, name) {
				t.Errorf("backend.yml does not run the darwin-only test %s", name)
			}
		}
	}
	if darwinTests == 0 {
		t.Error("found no darwin-tagged tests in internal/cli; this gate is watching nothing")
	}
}
