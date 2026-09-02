// Package observability checks that the shipped dashboard and alert rules only
// reference metrics and labels pgBase actually exposes. promtool proves the
// PromQL parses; nothing else proves it selects anything real, and a renamed
// label produces a permanently empty panel or a permanently silent alert.
package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Prometheus adds these at scrape time, so they are legitimate selectors even
// though the exposition never contains them.
var scrapeLabels = map[string]struct{}{"job": {}, "instance": {}}

var (
	seriesLine       = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)(?:\{([^}]*)\})?\s`)
	exposedLabelName = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)=`)
	pgbaseSelector   = regexp.MustCompile(`(pgbase_[a-zA-Z0-9_]*)\s*(?:\{([^}]*)\})?`)
	queryLabelName   = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)`)
	groupingClause   = regexp.MustCompile(`(?:without|by)\s*\(([^)]*)\)`)
)

// metricContract is the exposition golden reduced to what a query may select.
type metricContract map[string]map[string]struct{}

func loadMetricContract(t *testing.T) metricContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "observability", "testdata", "metrics.golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	contract := make(metricContract)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		match := seriesLine.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("golden exposition line is not a series: %q", line)
		}
		labels, ok := contract[match[1]]
		if !ok {
			labels = make(map[string]struct{})
			contract[match[1]] = labels
		}
		for _, label := range exposedLabelName.FindAllStringSubmatch(match[2], -1) {
			labels[label[1]] = struct{}{}
		}
	}
	if len(contract) == 0 {
		t.Fatal("the golden exposition declares no series")
	}
	return contract
}

// selectable resolves the names a query may use. A histogram is queried through
// its _bucket/_sum/_count series and a counter through its bare name, so the
// contract accepts the exposed name and the base name of any suffixed series.
func (c metricContract) selectable(name string) (map[string]struct{}, bool) {
	if labels, ok := c[name]; ok {
		return labels, true
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if labels, ok := c[name+suffix]; ok {
			return labels, true
		}
	}
	return nil, false
}

func TestDashboardAndAlertsOnlySelectExposedMetricsAndLabels(t *testing.T) {
	contract := loadMetricContract(t)
	for _, source := range []struct {
		name        string
		expressions []string
	}{
		{name: "grafana-dashboard.json", expressions: dashboardExpressions(t)},
		{name: "prometheus-alerts.yml", expressions: alertExpressions(t)},
	} {
		t.Run(source.name, func(t *testing.T) {
			if len(source.expressions) == 0 {
				t.Fatalf("%s contributed no expressions", source.name)
			}
			referenced := 0
			for _, expression := range source.expressions {
				for _, selector := range pgbaseSelector.FindAllStringSubmatch(expression, -1) {
					referenced++
					metric := selector[1]
					allowed, exposed := contract.selectable(metric)
					if !exposed {
						t.Errorf("%s selects %s, which pgBase does not expose", source.name, metric)
						continue
					}
					for _, label := range queryLabelName.FindAllStringSubmatch(selector[2], -1) {
						if _, scrape := scrapeLabels[label[1]]; scrape {
							continue
						}
						if _, ok := allowed[label[1]]; !ok {
							t.Errorf("%s filters %s on label %q, which the series does not carry (has %v)",
								source.name, metric, label[1], sortedNames(allowed))
						}
					}
				}
				assertGroupingLabelsExist(t, source.name, expression, contract)
			}
			if referenced == 0 {
				t.Errorf("%s references no pgBase metric at all", source.name)
			}
		})
	}
}

// assertGroupingLabelsExist checks the labels an aggregation names against the
// union of labels carried by the metrics in the same expression, which is what
// a rename would break.
func assertGroupingLabelsExist(t *testing.T, source, expression string, contract metricContract) {
	t.Helper()
	available := make(map[string]struct{})
	for _, selector := range pgbaseSelector.FindAllStringSubmatch(expression, -1) {
		labels, exposed := contract.selectable(selector[1])
		if !exposed {
			return
		}
		for label := range labels {
			available[label] = struct{}{}
		}
	}
	if len(available) == 0 {
		return
	}
	for _, clause := range groupingClause.FindAllStringSubmatch(expression, -1) {
		for _, label := range strings.Split(clause[1], ",") {
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			if _, scrape := scrapeLabels[label]; scrape {
				continue
			}
			if _, ok := available[label]; !ok {
				t.Errorf("%s aggregates on label %q, which no selected series carries (has %v)",
					source, label, sortedNames(available))
			}
		}
	}
}

func dashboardExpressions(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deployments", "observability", "grafana-dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard any
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("the dashboard is not valid JSON: %v", err)
	}
	var expressions []string
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if expression, ok := typed["expr"].(string); ok {
				expressions = append(expressions, expression)
			}
			for _, value := range typed {
				walk(value)
			}
		case []any:
			for _, value := range typed {
				walk(value)
			}
		}
	}
	walk(dashboard)
	return expressions
}

// The alert rules are read as text rather than parsed: promtool already
// validates their structure, and every pgbase_ token in the file is a metric
// reference, so a lexical scan cannot miss one.
func alertExpressions(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deployments", "observability", "prometheus-alerts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return []string{string(raw)}
}

func sortedNames(values map[string]struct{}) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
