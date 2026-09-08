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

// The bundled profile's statement timeout is the value an operator runs
// without an environment file, and it is the one the Go options layer
// defaults to. The end-to-end suite deliberately raises it so a drain test can
// hold a query open past the shutdown window, so nothing there exercises the
// shipped default any more; this keeps the two in step cheaply.
func TestComposeStatementTimeoutDefaultMatchesTheApplicationDefault(t *testing.T) {
	const applicationDefault = 5 * time.Second

	compose := readFile(t, "docker-compose.yml")
	if got := interpolationDefault(t, compose, "PGBASE_STATEMENT_TIMEOUT"); got != applicationDefault {
		t.Errorf("docker-compose.yml statement timeout default = %s, want the application default %s",
			got, applicationDefault)
	}
	example := readFile(t, ".env.example")
	if want := "PGBASE_STATEMENT_TIMEOUT=" + applicationDefault.String(); !strings.Contains(example, want) {
		t.Errorf(".env.example is missing %q", want)
	}
}

func TestComposeSchemaApplyTimeoutDefaultMatchesTheApplicationDefault(t *testing.T) {
	const applicationDefault = 10 * time.Minute

	compose := readFile(t, "docker-compose.yml")
	if got := interpolationDefault(t, compose, "PGBASE_SCHEMA_APPLY_TIMEOUT"); got != applicationDefault {
		t.Errorf("docker-compose.yml schema apply timeout default = %s, want the application default %s",
			got, applicationDefault)
	}
	example := readFile(t, ".env.example")
	match := regexp.MustCompile(`(?m)^PGBASE_SCHEMA_APPLY_TIMEOUT=(.+)$`).FindStringSubmatch(example)
	if match == nil {
		t.Fatal(".env.example does not set PGBASE_SCHEMA_APPLY_TIMEOUT")
	}
	value, err := time.ParseDuration(strings.TrimSpace(match[1]))
	if err != nil || value != applicationDefault {
		t.Fatalf(".env.example schema apply timeout = %q (%v), want %s", match[1], err, applicationDefault)
	}
}

func TestBundledComposeMakesItsLocalPlaintextDatabaseExceptionExplicit(t *testing.T) {
	compose := readFile(t, "docker-compose.yml")
	for _, contract := range []string{
		"PGBASE_DATABASE_URL: postgres://pgbase:${PGBASE_POSTGRES_PASSWORD}@postgres:5432/pgbase?sslmode=disable",
		"PGBASE_ALLOW_INSECURE_DATABASE: \"true\"",
		"PGBASE_HTTP_ADDRESS: :8090",
	} {
		if !strings.Contains(compose, contract) {
			t.Errorf("docker-compose.yml is missing %q", contract)
		}
	}
}

func TestBundledComposeAppliesConfigurableNonSwarmRuntimeCeilings(t *testing.T) {
	compose := readFile(t, "docker-compose.yml")
	example := readFile(t, ".env.example")
	for key, value := range map[string]string{
		"PGBASE_APP_CPUS":              "2.0",
		"PGBASE_APP_MEMORY_LIMIT":      "1g",
		"PGBASE_APP_PIDS_LIMIT":        "256",
		"PGBASE_POSTGRES_CPUS":         "2.0",
		"PGBASE_POSTGRES_MEMORY_LIMIT": "1g",
		"PGBASE_POSTGRES_PIDS_LIMIT":   "256",
	} {
		if !strings.Contains(compose, "${"+key+":-"+value+"}") {
			t.Errorf("docker-compose.yml is missing the configurable %s default", key)
		}
		if !strings.Contains(example, key+"="+value) {
			t.Errorf(".env.example is missing %s=%s", key, value)
		}
	}
	for _, key := range []string{"cpus:", "mem_limit:", "pids_limit:"} {
		if got := strings.Count(compose, key); got != 2 {
			t.Errorf("docker-compose.yml %s occurrences = %d, want one per service", key, got)
		}
	}
	if strings.Contains(compose, "deploy:\n") || strings.Contains(compose, "deploy:\r\n") {
		t.Fatal("bundled Compose resource ceilings must not rely on the optional deploy section")
	}
}
