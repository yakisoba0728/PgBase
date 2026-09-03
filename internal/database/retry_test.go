package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryUsesOneBoundedPostgresPolicy(t *testing.T) {
	for code, wantCause := range map[string]RetryCause{
		"0A000": RetryCauseCachedPlan,
		"40001": RetryCauseSerialization,
		"40P01": RetryCauseDeadlock,
	} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			var observed []struct {
				cause     RetryCause
				exhausted bool
			}
			err := Retry(context.Background(), func() error {
				calls++
				if calls == 1 {
					return fmt.Errorf("transaction failed: %w", &pgconn.PgError{Code: code, Message: "must not escape telemetry"})
				}
				return nil
			}, func(cause RetryCause, exhausted bool) {
				observed = append(observed, struct {
					cause     RetryCause
					exhausted bool
				}{cause: cause, exhausted: exhausted})
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(observed) != 1 || observed[0].cause != wantCause || observed[0].exhausted {
				t.Fatalf("calls=%d observations=%#v, want one non-exhausted %q retry", calls, observed, wantCause)
			}
		})
	}
}

func TestRetryReturnsNonRetryableErrorWithoutObservation(t *testing.T) {
	want := errors.New("validation failed")
	calls := 0
	observations := 0
	err := Retry(context.Background(), func() error {
		calls++
		return want
	}, func(RetryCause, bool) { observations++ })
	if !errors.Is(err, want) || calls != 1 || observations != 0 {
		t.Fatalf("Retry() error=%v calls=%d observations=%d", err, calls, observations)
	}
}

func TestRetryStopsImmediatelyWhenParentIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	observations := 0
	err := Retry(ctx, func() error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40001", Message: "forced serialization"}
	}, func(RetryCause, bool) { observations++ })
	if !errors.Is(err, context.Canceled) || calls != 1 || observations != 0 {
		t.Fatalf("Retry() error=%v calls=%d observations=%d", err, calls, observations)
	}
}

func TestRetryDoesNotInvokeOperationWhenAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Retry(ctx, func() error {
		calls++
		return nil
	}, nil)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("Retry() error=%v calls=%d, want canceled before zero calls", err, calls)
	}
}

func TestRetryExhaustionUsesTheBoundedAttemptCount(t *testing.T) {
	calls := 0
	observations := 0
	exhausted := 0
	err := retry(context.Background(), func() error {
		calls++
		return &pgconn.PgError{Code: "40001", Message: "forced serialization"}
	}, func(_ RetryCause, final bool) {
		observations++
		if final {
			exhausted++
		}
	}, func(int) error { return nil })
	if err == nil || calls != maximumRetries+1 || observations != maximumRetries+1 || exhausted != 1 {
		t.Fatalf("error=%v calls=%d observations=%d exhausted=%d", err, calls, observations, exhausted)
	}
}

func TestRetryDelayNeverExceedsTheCap(t *testing.T) {
	for attempt := 0; attempt < 256; attempt++ {
		delay := retryDelay(attempt)
		if delay <= 0 || delay > maximumRetryDelay {
			t.Fatalf("retryDelay(%d) = %s, want (0, %s]", attempt, delay, maximumRetryDelay)
		}
		if attempt >= 6 && delay < maximumRetryDelay/2 {
			t.Fatalf("retryDelay(%d) = %s, want capped jitter at least %s", attempt, delay, maximumRetryDelay/2)
		}
	}
	if maximumRetryDelay != 100*time.Millisecond {
		t.Fatalf("maximum retry delay = %s", maximumRetryDelay)
	}
}
