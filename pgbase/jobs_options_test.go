package pgbase

import (
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJobRunOccurrenceKeyIsStableAcrossAttemptsAndChangesWithSchedule(t *testing.T) {
	jobID := uuid.New()
	scheduledAt := time.Date(2026, 9, 10, 6, 0, 0, 123456000, time.UTC)
	first := JobRun{JobID: jobID, FenceGeneration: 1, ScheduledAt: scheduledAt}
	retry := JobRun{JobID: jobID, FenceGeneration: 2, ScheduledAt: scheduledAt}
	next := JobRun{JobID: jobID, FenceGeneration: 3, ScheduledAt: scheduledAt.Add(time.Hour)}

	if first.OccurrenceKey() != retry.OccurrenceKey() {
		t.Fatalf("retry occurrence key changed: %q != %q", first.OccurrenceKey(), retry.OccurrenceKey())
	}
	if retry.OccurrenceKey() == next.OccurrenceKey() {
		t.Fatalf("next schedule reused occurrence key %q", next.OccurrenceKey())
	}
	if first.RunKey() == retry.RunKey() {
		t.Fatalf("retry reused attempt run key %q", retry.RunKey())
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._~-]+$`).MatchString(first.OccurrenceKey()) {
		t.Fatalf("occurrence key is not safe for PgBase idempotency: %q", first.OccurrenceKey())
	}
}
