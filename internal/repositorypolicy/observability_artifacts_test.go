package repositorypolicy

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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
	for _, link := range links {
		if _, err := os.Stat(filepath.Clean(filepath.Join(filepath.Dir(link.from), link.target))); err != nil {
			t.Errorf("documentation link from %s to %s does not resolve: %v", link.from, link.target, err)
		}
	}
}

func TestHTTPMetricRouteCardinalityBudget(t *testing.T) {
	routerPath := filepath.Join("..", "api", "router.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), routerPath, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	registeredRoutes := 0
	methodAgnosticRoutes := 0
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(selector.Sel.Name, "Handle") {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		literal, literalOK := call.Args[0].(*ast.BasicLit)
		if !ok || receiver.Name != "mux" || !literalOK || literal.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Errorf("decode route pattern %s: %v", literal.Value, err)
			return true
		}
		registeredRoutes++
		if !strings.Contains(pattern, " ") {
			methodAgnosticRoutes++
		}
		return true
	})
	if registeredRoutes == 0 {
		t.Fatal("route cardinality policy found no direct mux registrations")
	}
	if registeredRoutes > 32 || methodAgnosticRoutes > 3 {
		t.Fatalf("HTTP metric route budget exceeded: routes=%d/32 method-agnostic=%d/3; update the telemetry design and series budget before expanding", registeredRoutes, methodAgnosticRoutes)
	}
}
