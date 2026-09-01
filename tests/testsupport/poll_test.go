package testsupport

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollRetriesUntilTheConditionSucceeds(t *testing.T) {
	var attempts atomic.Int32
	err := Poll(context.Background(), time.Second, time.Millisecond, 100*time.Millisecond, func(context.Context) (bool, error) {
		if attempts.Add(1) == 3 {
			return true, nil
		}
		return false, errors.New("not yet")
	})
	if err != nil || attempts.Load() != 3 {
		t.Fatalf("Poll() error = %v, attempts = %d; want nil/3", err, attempts.Load())
	}
}

func TestPollStopsImmediatelyWhenItsParentIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Poll(ctx, time.Hour, time.Hour, time.Minute, func(context.Context) (bool, error) {
			close(started)
			return false, errors.New("still waiting")
		})
	}()
	<-started
	startedAt := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Poll() error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
			t.Fatalf("Poll() cancellation took %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("Poll did not stop after parent cancellation")
	}
}

func TestPollReturnsDeadlineAndLastAttemptError(t *testing.T) {
	err := Poll(context.Background(), 20*time.Millisecond, time.Millisecond, 5*time.Millisecond, func(ctx context.Context) (bool, error) {
		<-ctx.Done()
		return false, errors.New("probe failed")
	})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "probe failed") {
		t.Fatalf("Poll() error = %v, want deadline plus last probe error", err)
	}
}
