//go:build compose

// Package compose validates the rendered Compose profiles an operator actually
// deploys. The pure contract in deployments/ pins the file's interpolation
// defaults; only Compose itself can prove what those defaults render to.
package compose

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every rendered profile must keep the container stop grace at least this far
// above the application shutdown timeout, so a Compose SIGKILL can never
// interrupt a drain that pgBase still considers in progress.
const stopGraceMargin = 5 * time.Second

type renderedService struct {
	StopGracePeriod string            `json:"stop_grace_period"`
	Environment     map[string]string `json:"environment"`
}

type renderedProject struct {
	Services map[string]renderedService `json:"services"`
}

// Compose refuses to render without these, and they carry no bearing on the
// shutdown contract under test.
func requiredSecrets() []string {
	return []string{
		"PGBASE_POSTGRES_PASSWORD=compose-policy-postgres-password",
		"PGBASE_SETUP_TOKEN=compose-policy-setup-token-0123456789abcdef",
		"PGBASE_TOKEN_SECRET=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
}

func TestRenderedComposeProfilesKeepTheStopGraceMargin(t *testing.T) {
	tests := []struct {
		name         string
		overrides    []string
		wantShutdown time.Duration
		wantGrace    time.Duration
	}{
		{name: "default", wantShutdown: 15 * time.Second, wantGrace: 20 * time.Second},
		{
			name:         "override",
			overrides:    []string{"PGBASE_SHUTDOWN_TIMEOUT=40s", "PGBASE_STOP_GRACE_PERIOD=45s"},
			wantShutdown: 40 * time.Second,
			wantGrace:    45 * time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := renderService(t, test.overrides...)
			shutdown := parseDuration(t, "PGBASE_SHUTDOWN_TIMEOUT", service.Environment["PGBASE_SHUTDOWN_TIMEOUT"])
			grace := parseDuration(t, "PGBASE_STOP_GRACE_PERIOD", service.Environment["PGBASE_STOP_GRACE_PERIOD"])
			containerGrace := parseDuration(t, "stop_grace_period", service.StopGracePeriod)
			if shutdown != test.wantShutdown || grace != test.wantGrace {
				t.Fatalf("rendered shutdown/grace = %s/%s, want %s/%s", shutdown, grace, test.wantShutdown, test.wantGrace)
			}
			// One variable must drive both the declared application grace and
			// the container stop grace, or the two can silently diverge.
			if containerGrace != grace {
				t.Fatalf("stop_grace_period = %s, want the configured grace %s", containerGrace, grace)
			}
			if grace-shutdown < stopGraceMargin {
				t.Fatalf("grace %s - shutdown %s = %s, want at least %s", grace, shutdown, grace-shutdown, stopGraceMargin)
			}
		})
	}
}

func TestRenderedComposeRejectsAMalformedStopGrace(t *testing.T) {
	output, err := renderProject(t, "PGBASE_STOP_GRACE_PERIOD=not-a-duration")
	if err == nil {
		t.Fatalf("Compose rendered a malformed stop grace:\n%s", output)
	}
	if !strings.Contains(output, "stop_grace_period") {
		t.Errorf("rejection did not name stop_grace_period:\n%s", output)
	}
}

// A too-small margin still renders, because Compose only validates duration
// syntax. The rendered profile therefore has to be checked arithmetically; the
// application enforces the same rule at startup
// (internal/config.TestLoadEnvRequiresComposeStopGraceMargin).
func TestRenderedComposeSurfacesATooSmallMargin(t *testing.T) {
	service := renderService(t, "PGBASE_SHUTDOWN_TIMEOUT=40s", "PGBASE_STOP_GRACE_PERIOD=42s")
	shutdown := parseDuration(t, "PGBASE_SHUTDOWN_TIMEOUT", service.Environment["PGBASE_SHUTDOWN_TIMEOUT"])
	grace := parseDuration(t, "stop_grace_period", service.StopGracePeriod)
	if grace-shutdown >= stopGraceMargin {
		t.Fatalf("margin %s was expected to violate the %s policy", grace-shutdown, stopGraceMargin)
	}
}

func renderService(t *testing.T, overrides ...string) renderedService {
	t.Helper()
	output, err := renderProject(t, overrides...)
	if err != nil {
		t.Fatalf("render Compose profile: %v\n%s", err, output)
	}
	var project renderedProject
	if err := json.Unmarshal([]byte(output), &project); err != nil {
		t.Fatalf("decode rendered Compose profile: %v", err)
	}
	service, ok := project.Services["pgbase"]
	if !ok {
		t.Fatal("rendered Compose profile has no pgbase service")
	}
	return service
}

func renderProject(t *testing.T, overrides ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "compose",
		"--file", filepath.Join(repositoryRoot(t), "deployments", "docker-compose.yml"),
		"config", "--format", "json")
	// A developer .env or exported override must not decide what CI renders.
	command.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}, requiredSecrets()...)
	command.Env = append(command.Env, overrides...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func parseDuration(t *testing.T, name, raw string) time.Duration {
	t.Helper()
	value, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("rendered %s = %q: %v", name, raw, err)
	}
	return value
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
