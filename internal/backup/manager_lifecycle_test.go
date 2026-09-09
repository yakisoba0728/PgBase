package backup

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
