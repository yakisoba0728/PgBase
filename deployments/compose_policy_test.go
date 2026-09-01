package deployments_test

import (
	"os"
	"strings"
	"testing"
)

func TestComposeShutdownGraceUsesOneConfigurableDefault(t *testing.T) {
	compose, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PGBASE_SHUTDOWN_TIMEOUT: ${PGBASE_SHUTDOWN_TIMEOUT:-15s}",
		"PGBASE_STOP_GRACE_PERIOD: ${PGBASE_STOP_GRACE_PERIOD:-20s}",
		"stop_grace_period: ${PGBASE_STOP_GRACE_PERIOD:-20s}",
	}
	for _, contract := range want {
		if !strings.Contains(string(compose), contract) {
			t.Errorf("docker-compose.yml is missing %q", contract)
		}
	}

	example, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"PGBASE_SHUTDOWN_TIMEOUT=15s", "PGBASE_STOP_GRACE_PERIOD=20s"} {
		if !strings.Contains(string(example), value) {
			t.Errorf(".env.example is missing %q", value)
		}
	}
}
