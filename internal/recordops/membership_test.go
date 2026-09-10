package recordops

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/membership"
)

func TestMembershipFailuresBecomeStableAvailabilityErrors(t *testing.T) {
	causes := []error{
		membership.ErrUnavailable,
		membership.ErrInvalidState,
		membership.ErrWriterTooOld,
		membership.ErrMembershipDrift,
		errors.New("secret membership database detail"),
	}
	for _, cause := range causes {
		err := unavailableMembership(cause)
		if !IsCode(err, CodeUnavailable) || err.Error() != string(CodeUnavailable) {
			t.Fatalf("unavailableMembership(%v) = %#v", cause, err)
		}
		for _, sentinel := range causes[:4] {
			if errors.Is(err, sentinel) {
				t.Fatalf("availability error leaked membership sentinel %v", sentinel)
			}
		}
	}
	if err := unavailableMembership(nil); err != nil {
		t.Fatalf("unavailableMembership(nil) = %v", err)
	}
}

func TestTransactionalMembershipFailureKeepsRetryCauseOnlyInsideBoundary(t *testing.T) {
	cause := &pgconn.PgError{Code: "40001", Message: "secret membership serialization detail"}
	internal := transactionalMembership(cause)
	var postgresError *pgconn.PgError
	if !errors.As(internal, &postgresError) || postgresError != cause {
		t.Fatalf("internal membership error lost retry cause: %#v", internal)
	}
	external := membershipBoundaryError(internal)
	if !IsCode(external, CodeUnavailable) || errors.As(external, &postgresError) ||
		external.Error() != string(CodeUnavailable) {
		t.Fatalf("membership boundary error leaked retry cause: %#v", external)
	}
}
