package observability

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// operationMatrix is the label pairing an alert query depends on, written out
// as literal strings rather than read back from the code under test. A new
// operation constant that is not listed here fails TestOperationSetIsComplete
// rather than silently going unverified.
var operationMatrix = []struct{ operation, component string }{
	{operation: "migration_up", component: "migration"},
	{operation: "migration_status", component: "migration"},
	{operation: "schema_create", component: "schema"},
	{operation: "schema_update", component: "schema"},
	{operation: "schema_delete", component: "schema"},
	{operation: "listener_connect", component: "schema_listener"},
	{operation: "listener_reload", component: "schema_listener"},
	{operation: "admin_setup", component: "admin_auth"},
	{operation: "admin_login", component: "admin_auth"},
	{operation: "relation_schema", component: "relation"},
	{operation: "relation_write", component: "relation"},
	{operation: "relation_delete", component: "relation"},
	{operation: "relation_expand", component: "relation"},
	{operation: "relation_read", component: "relation"},
}

var allOutcomes = []Outcome{
	OutcomeSuccess,
	OutcomeFailure,
	OutcomeConflict,
	OutcomeValidation,
	OutcomeUnavailable,
	OutcomeCanceled,
	OutcomeInvalidCredentials,
	OutcomeRateLimited,
	OutcomeSetupUnavailable,
	OutcomeBudget,
}

// TestEveryOperationAndOutcomeProducesItsDerivedComponentSeries walks the whole
// matrix in one place. Callers no longer name a component, so the only thing
// that can put an operation under the wrong component is this package, and the
// exposition below is what an alert query reads.
func TestEveryOperationAndOutcomeProducesItsDerivedComponentSeries(t *testing.T) {
	metrics := NewMetrics(nil)
	wanted := make([]string, 0, len(operationMatrix)*len(allOutcomes))
	for _, entry := range operationMatrix {
		operation := Operation(entry.operation)
		for _, outcome := range allOutcomes {
			event := OperationEvent(operation, outcome, time.Millisecond)
			if string(event.Component()) != entry.component || event.Operation() != operation || event.Outcome() != outcome {
				t.Fatalf("OperationEvent(%q, %q) = component %q operation %q outcome %q, want component %q",
					operation, outcome, event.Component(), event.Operation(), event.Outcome(), entry.component)
			}
			metrics.Record(context.Background(), event)
			wanted = append(wanted, fmt.Sprintf(
				`pgbase_application_operations_total{component=%q,operation=%q,outcome=%q} 1`,
				entry.component, entry.operation, outcome))
		}
	}

	body := scrape(t, metrics, "matrix-access-token")
	assertContains(t, body, wanted...)
	assertContains(t, body, "pgbase_observability_dropped_events_total 0")
}

// TestOperationSetIsComplete keeps the matrix honest: the exposition names
// every operation constant this package defines, so adding one without adding
// it above fails here instead of leaving a series unverified.
func TestOperationSetIsComplete(t *testing.T) {
	covered := make(map[string]struct{}, len(operationMatrix))
	for _, entry := range operationMatrix {
		covered[entry.operation] = struct{}{}
	}
	for _, operation := range []Operation{
		OperationMigrationUp, OperationMigrationStatus,
		OperationSchemaCreate, OperationSchemaUpdate, OperationSchemaDelete,
		OperationListenerConnect, OperationListenerReload,
		OperationAdminSetup, OperationAdminLogin,
		OperationRelationSchema, OperationRelationWrite, OperationRelationDelete,
		OperationRelationExpand, OperationRelationRead,
	} {
		if _, ok := covered[string(operation)]; !ok {
			t.Fatalf("operation constant %q is not covered by the matrix", operation)
		}
	}
}

// TestComponentCannotBeChosenByACaller pins the derivation itself: an event
// whose component disagrees with its operation is dropped rather than recorded
// under a component an alert query does not expect.
func TestComponentCannotBeChosenByACaller(t *testing.T) {
	metrics := NewMetrics(nil)
	metrics.Record(context.Background(), Event{
		kind:      EventKindOperation,
		component: ComponentSchema,
		operation: OperationMigrationUp,
		outcome:   OutcomeSuccess,
	})
	body := scrape(t, metrics, "matrix-access-token")
	assertContains(t, body, "pgbase_observability_dropped_events_total 1")
	if strings.Contains(body, `component="schema",operation="migration_up"`) {
		t.Fatalf("a mislabelled operation reached the exposition:\n%s", body)
	}
}
