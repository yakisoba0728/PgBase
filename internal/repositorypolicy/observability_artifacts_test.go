package repositorypolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationalTelemetryArtifactsAndLinks(t *testing.T) {
	root := filepath.Join("..", "..")
	dashboardPath := filepath.Join(root, "deployments", "observability", "grafana-dashboard.json")
	dashboard, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(dashboard, &decoded); err != nil {
		t.Fatalf("parse Grafana dashboard: %v", err)
	}

	alerts, err := os.ReadFile(filepath.Join(root, "deployments", "observability", "prometheus-alerts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{
		"up{job=\"pgbase\"}",
		"pgbase_application_readiness",
		"pgbase_http_requests_total",
		"pgbase_database_pool_acquired_connections",
		"pgbase_database_transaction_retries_total",
		"pgbase_observability_dropped_events_total",
	} {
		if !strings.Contains(string(alerts), metric) {
			t.Fatalf("Prometheus rules omit required signal %q", metric)
		}
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "backend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"jq empty deployments/observability/grafana-dashboard.json", "--entrypoint=/bin/promtool", "prom/prometheus@sha256:"} {
		if !strings.Contains(string(workflow), marker) {
			t.Fatalf("backend workflow does not validate telemetry artifact with %q", marker)
		}
	}

	links := []struct {
		from   string
		target string
	}{
		{from: filepath.Join(root, "docs", "superpowers", "specs", "2026-08-31-operational-telemetry-design.md"), target: filepath.Join("..", "..", "pocketbase-reference-research-2026-08-31.md")},
		{from: filepath.Join(root, "docs", "superpowers", "plans", "2026-08-31-operational-telemetry.md"), target: filepath.Join("..", "specs", "2026-08-31-operational-telemetry-design.md")},
		{from: filepath.Join(root, "docs", "deployment.md"), target: filepath.Join("..", "deployments", "observability", "grafana-dashboard.json")},
		{from: filepath.Join(root, "docs", "deployment.md"), target: filepath.Join("..", "deployments", "observability", "prometheus-alerts.yml")},
	}
	// The source document has to be read, not just used for its directory:
	// checking only that the target file exists passes even when the document
	// or the link text is gone.
	for _, link := range links {
		source, err := os.ReadFile(link.from)
		if err != nil {
			t.Errorf("read documentation source %s: %v", link.from, err)
			continue
		}
		if !strings.Contains(string(source), filepath.ToSlash(link.target)) {
			t.Errorf("documentation %s no longer links to %s", link.from, link.target)
			continue
		}
		if _, err := os.Stat(filepath.Clean(filepath.Join(filepath.Dir(link.from), link.target))); err != nil {
			t.Errorf("documentation link from %s to %s does not resolve: %v", link.from, link.target, err)
		}
	}
}

// The HTTP metric series budget moved into the route registry itself, where
// internal/api's TestRouteRegistryIsCompleteUniqueAndWithinTheMetricBudget
// checks the declared routes rather than the shape of registration calls.
