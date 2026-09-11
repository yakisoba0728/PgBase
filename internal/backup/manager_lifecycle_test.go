package backup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuardedRestoreRetainsAdmissionUntilBackgroundCompletion(t *testing.T) {
	entered, proceed, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releases atomic.Int32
	m, err := NewManager(ManagerOptions{
		Directory: t.TempDir(),
		Create:    func(_ context.Context, directory string) error { makeArchiveFixture(t, directory); return nil },
		Restore: func(ctx context.Context, _ string) error {
			close(entered)
			select {
			case <-proceed:
				return errors.New("activation failed")
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	created, err := m.Create(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	release := func() { releases.Add(1); close(released) }
	job, err := m.RestoreWithGuard(t.Context(), created.BackupID, release)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not start")
	}
	if releases.Load() != 0 {
		t.Fatal("admission released before restore finished")
	}
	close(proceed)
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not release admission")
	}
	result, err := m.Job(t.Context(), job.ID)
	if err != nil || result.Status != "failed" || releases.Load() != 1 {
		t.Fatalf("result=%+v releases=%d err=%v", result, releases.Load(), err)
	}
	if _, err := m.RestoreWithGuard(t.Context(), "invalid", release); err == nil {
		t.Fatal("invalid backup accepted")
	}
	if releases.Load() != 1 {
		t.Fatal("failed admission took ownership of guard")
	}
}

type managerLifecycleStub struct {
	veto   string
	events []*LifecycleEvent
	after  chan error
}

func (h *managerLifecycleStub) BeginBackupOperation(_ context.Context, event *LifecycleEvent) (func(error), error) {
	h.events = append(h.events, event)
	finish := func(err error) { h.after <- err }
	if event.Operation == h.veto {
		return finish, errors.New("backup veto")
	}
	return finish, nil
}

func TestManagerLifecycleVetoAndFinalReceipt(t *testing.T) {
	hooks := &managerLifecycleStub{veto: "create", after: make(chan error, 4)}
	manager, err := NewManager(ManagerOptions{Directory: t.TempDir(), Lifecycle: hooks, Create: func(context.Context, string) error { t.Fatal("veto reached create"); return nil }, Restore: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if _, err := manager.Create(context.Background()); err == nil {
		t.Fatal("vetoed create succeeded")
	}
	if err := <-hooks.after; err == nil {
		t.Fatal("veto After observed success")
	}

	hooks.veto = ""
	manager.options.Create = func(_ context.Context, directory string) error { makeArchiveFixture(t, directory); return nil }
	job, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-hooks.after:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("create After did not run")
	}
	// After observes the final receipt before execute releases its reservation.
	// Wait for that worker to exit before starting the next operation.
	manager.wg.Wait()
	if len(hooks.events) != 2 || hooks.events[1].Job.ID != job.ID || hooks.events[1].Job.Status != "completed" {
		t.Fatalf("events=%+v", hooks.events)
	}
	restored, err := manager.Restore(context.Background(), job.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-hooks.after; err != nil {
		t.Fatal(err)
	}
	manager.wg.Wait()
	if len(hooks.events) != 3 || hooks.events[2].Operation != "restore" || hooks.events[2].Job.ID != restored.ID || hooks.events[2].Job.Status != "completed" {
		t.Fatalf("restore events=%+v", hooks.events)
	}
	if err := manager.Delete(context.Background(), job.BackupID); err != nil {
		t.Fatal(err)
	}
	if err := <-hooks.after; err != nil {
		t.Fatal(err)
	}
	if len(hooks.events) != 4 || hooks.events[3].Operation != "delete" {
		t.Fatalf("delete events=%+v", hooks.events)
	}
}
