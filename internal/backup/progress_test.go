package backup

import (
	"context"
	"testing"
	"time"
)

func TestProgressRefreshesJobUpdatedAt(t *testing.T) {
	old := time.Now().Add(-time.Hour).UTC()
	manager := &Manager{
		options: ManagerOptions{Directory: t.TempDir()},
		jobs:    map[string]Job{"job": {ID: "job", Phase: "starting", UpdatedAt: old}},
	}
	ctx := manager.progressContext(context.Background(), "job")
	if err := Progress(ctx, "copying"); err != nil {
		t.Fatal(err)
	}
	job := manager.jobs["job"]
	if job.Phase != "copying" || !job.UpdatedAt.After(old) {
		t.Fatalf("progress did not refresh receipt: %+v", job)
	}
}
