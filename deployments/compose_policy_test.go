package deployments_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ComposeStopGraceMargin is the margin the container stop grace must keep over
// the application shutdown timeout. It exists so an orchestrator SIGKILL can
// never land while pgBase is still inside its own bounded drain.
const ComposeStopGraceMargin = 5 * time.Second

func TestComposeShutdownGraceUsesOneConfigurableDefault(t *testing.T) {
	compose := readFile(t, "docker-compose.yml")
	want := []string{
		"PGBASE_SHUTDOWN_TIMEOUT: ${PGBASE_SHUTDOWN_TIMEOUT:-15s}",
		"PGBASE_STOP_GRACE_PERIOD: ${PGBASE_STOP_GRACE_PERIOD:-20s}",
		"stop_grace_period: ${PGBASE_STOP_GRACE_PERIOD:-20s}",
	}
	for _, contract := range want {
		if !strings.Contains(compose, contract) {
			t.Errorf("docker-compose.yml is missing %q", contract)
		}
	}

	example := readFile(t, ".env.example")
	for _, value := range []string{"PGBASE_SHUTDOWN_TIMEOUT=15s", "PGBASE_STOP_GRACE_PERIOD=20s"} {
		if !strings.Contains(example, value) {
			t.Errorf(".env.example is missing %q", value)
		}
	}
}

// The Compose interpolation defaults are what an operator gets without an
// environment file, so they carry the same margin the application enforces.
func TestComposeInterpolationDefaultsKeepTheStopGraceMargin(t *testing.T) {
	compose := readFile(t, "docker-compose.yml")
	shutdown := interpolationDefault(t, compose, "PGBASE_SHUTDOWN_TIMEOUT")
	grace := interpolationDefault(t, compose, "PGBASE_STOP_GRACE_PERIOD")
	if grace-shutdown < ComposeStopGraceMargin {
		t.Fatalf("default stop grace %s - shutdown timeout %s = %s, want at least %s",
			grace, shutdown, grace-shutdown, ComposeStopGraceMargin)
	}

	example := readFile(t, ".env.example")
	for key, want := range map[string]time.Duration{
		"PGBASE_SHUTDOWN_TIMEOUT":  shutdown,
		"PGBASE_STOP_GRACE_PERIOD": grace,
	} {
		match := regexp.MustCompile(`(?m)^` + key + `=(.+)$`).FindStringSubmatch(example)
		if match == nil {
			t.Fatalf(".env.example does not set %s", key)
		}
		value, err := time.ParseDuration(strings.TrimSpace(match[1]))
		if err != nil {
			t.Fatalf(".env.example %s: %v", key, err)
		}
		if value != want {
			t.Errorf(".env.example %s = %s, want the Compose default %s", key, value, want)
		}
	}
}

func interpolationDefault(t *testing.T, compose, key string) time.Duration {
	t.Helper()
	match := regexp.MustCompile(`\$\{` + key + `:-([^}]+)\}`).FindStringSubmatch(compose)
	if match == nil {
		t.Fatalf("docker-compose.yml has no interpolation default for %s", key)
	}
	value, err := time.ParseDuration(match[1])
	if err != nil {
		t.Fatalf("docker-compose.yml %s default: %v", key, err)
	}
	return value
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
