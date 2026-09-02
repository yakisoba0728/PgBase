package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

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
