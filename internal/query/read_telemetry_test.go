package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

// Relation-path telemetry has to count the work the plan actually does. A bare
// single relation sort orders on the root's own column and a bare multi
// relation sort is refused from its cardinality, so neither reads a target and
// neither is relation-path work. A relation filter reads one either way.
func TestRelationTelemetryCountsBareFiltersAndDottedSortsOnly(t *testing.T) {
	t.Parallel()

	_, posts := relationFilterSortCatalog()
	for _, test := range []struct {
		name   string
		sort   string
		filter string
		want   bool
	}{
		{name: "bare single relation sort orders on the root column", sort: "owner"},
		{name: "bare multi relation sort never reaches a target", sort: "reviewers"},
		{name: "scalar sort", sort: "created_at"},
		{name: "dotted sort reads a target", sort: "owner.name", want: true},
		{name: "bare relation filter reads a target", filter: `owner = '11111111-1111-4111-8111-111111111111'`, want: true},
		{name: "dotted filter reads a target", filter: `owner.name = 'Ada'`, want: true},
		{name: "scalar filter", filter: `title = 'Ada'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var parsed filterNode
			if test.filter != "" {
				parsed = mustParseFilter(t, test.filter)
			}
			var keys []SortKey
			if test.sort != "" {
				requested, err := parseSort(test.sort)
				if err != nil {
					t.Fatalf("parseSort(%q) error = %v", test.sort, err)
				}
				keys = effectiveSortKeys(requested)
			}
			if got := requestAddressesRelationField(posts, parsed, keys); got != test.want {
				t.Fatalf("requestAddressesRelationField(sort=%q, filter=%q) = %t, want %t",
					test.sort, test.filter, got, test.want)
			}
		})
	}
}

func TestReadOperationOutcomeSeparatesMembershipAvailabilityAndCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want observability.Outcome
	}{
		{name: "unavailable", err: membership.ErrUnavailable, want: observability.OutcomeUnavailable},
		{name: "canceled unavailable", err: fmt.Errorf("%w: %w", membership.ErrUnavailable, context.Canceled), want: observability.OutcomeCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readOperationOutcome(test.err); got != test.want {
				t.Fatalf("readOperationOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}
