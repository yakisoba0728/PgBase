package backup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedCreateFailureNotifiesOnlyManagedJobs(t *testing.T) {
	var notifications atomic.Int32
	manager, err := NewManager(ManagerOptions{
		Directory: t.TempDir(),
		Create:    func(context.Context, string) error { return errors.New("fixture failure") },
		Restore:   func(context.Context, string) error { return nil },
		OnFailure: func(_ context.Context, job Job) {
			if !job.Managed || job.Status != "failed" {
				t.Errorf("unsafe failure callback job=%+v", job)
			}
			notifications.Add(1)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Shutdown(context.Background()) }()
	manual, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFinalJob(t, manager, manual.ID)
	if got := notifications.Load(); got != 0 {
		t.Fatalf("manual failure notifications=%d", got)
	}
	managed, err := manager.CreateManaged(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFinalJob(t, manager, managed.ID)
	if got := notifications.Load(); got != 1 {
		t.Fatalf("managed failure notifications=%d", got)
	}
}

func waitFinalJob(t *testing.T, manager *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("backup job did not finish")
}
