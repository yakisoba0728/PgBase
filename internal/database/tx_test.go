package database

import (
	"context"
	"testing"
	"time"
)

func TestBoundedCleanupContextAllowsCleanupAfterCancellationAndRetainsEarlierDeadline(t *testing.T) {
	canceledParent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	cleanupCtx, cancelCleanup := BoundedCleanupContext(canceledParent)
	defer cancelCleanup()
	select {
	case <-cleanupCtx.Done():
		t.Fatalf("cleanup context inherited cancellation without a deadline: %v", cleanupCtx.Err())
	default:
	}
	deadline, ok := cleanupCtx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Second {
		t.Fatalf("cleanup deadline = %v, present=%t", deadline, ok)
	}

	parentDeadline := time.Now().Add(25 * time.Millisecond)
	deadlineParent, cancelDeadlineParent := context.WithDeadline(context.Background(), parentDeadline)
	defer cancelDeadlineParent()
	earlierCleanup, cancelEarlierCleanup := BoundedCleanupContext(deadlineParent)
	defer cancelEarlierCleanup()
	gotDeadline, ok := earlierCleanup.Deadline()
	if !ok || gotDeadline.After(parentDeadline) {
		t.Fatalf("cleanup deadline = %v, want no later than %v", gotDeadline, parentDeadline)
	}
}
