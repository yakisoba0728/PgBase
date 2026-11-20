package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestScheduledBackupRecoversArchiveWithoutRecreatingIt(t *testing.T) {
	ctx := t.Context()
	id := uuid.NewString()
	repository := NewLocalRepository(t.TempDir())
	source := t.TempDir()
	makeArchiveFixture(t, source)
	var packed bytes.Buffer
	if err := Pack(ctx, source, &packed); err != nil {
		t.Fatal(err)
	}
	// The old publisher wrote the complete archive but stopped before its receipt.
	if err := os.WriteFile(repository.archive(id), packed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerOptions{Directory: t.TempDir(), Repository: repository,
		Create: func(context.Context, string) error {
			return errors.New("retry unexpectedly created a new backup")
		},
		Restore: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })

	for range 2 {
		if err = manager.RunScheduled(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	items, err := manager.List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("retained archives = %#v, err=%v", items, err)
	}
	actual, err := manager.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(actual)
	if err = errors.Join(readErr, actual.Close()); err != nil || !bytes.Equal(content, packed.Bytes()) {
		t.Fatalf("retained backup digest or bytes changed: %v", err)
	}
}

func TestScheduledBackupCancellationCancelsCreate(t *testing.T) {
	started := make(chan struct{})
	manager, err := NewManager(ManagerOptions{Directory: t.TempDir(),
		Create: func(ctx context.Context, _ string) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
		Restore: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.RunScheduled(ctx, uuid.NewString()) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduled backup cancellation = %v", err)
	}
}
