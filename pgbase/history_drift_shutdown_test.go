package pgbase

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/membership"
)

// onlyInjectedMembershipDrift is used only after a fixture deliberately removes
// required migration history. The writer may notice before or after readiness:
// both schedules are valid. Do not hide any unrelated joined shutdown failure.
func onlyInjectedMembershipDrift(err error) bool {
	if err == nil || err == membership.ErrInvalidState {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyInjectedMembershipDrift(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		child := wrapped.Unwrap()
		return child != nil && onlyInjectedMembershipDrift(child)
	default:
		return false
	}
}

func TestInjectedHistoryDriftDoesNotMaskOtherShutdownFailures(t *testing.T) {
	other := errors.New("unrelated shutdown failure")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"not observed by background runner", nil, true},
		{"observed drift", membership.ErrInvalidState, true},
		{"wrapped drift", fmt.Errorf("record membership writer: %w", membership.ErrInvalidState), true},
		{"joined drift", errors.Join(membership.ErrInvalidState, fmt.Errorf("writer: %w", membership.ErrInvalidState)), true},
		{"unrelated", other, false},
		{"joined unrelated", errors.Join(membership.ErrInvalidState, other), false},
		{"wrapped mixed", fmt.Errorf("shutdown: %w", errors.Join(membership.ErrInvalidState, other)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := onlyInjectedMembershipDrift(tc.err); got != tc.want {
				t.Fatalf("accepted=%v want=%v error=%v", got, tc.want, tc.err)
			}
		})
	}
}
