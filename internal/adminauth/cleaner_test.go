package adminauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanerRetriesTransientFailuresInsteadOfStopping(t *testing.T) {
	var calls atomic.Int32
	succeeded := make(chan struct{})
	var logs bytes.Buffer
	cleaner := &Cleaner{
		cleanFn: func(context.Context) error {
			if calls.Add(1) == 1 {
				return errors.New("temporary database failure")
			}
			close(succeeded)
			return nil
		},
		retryMin: time.Millisecond,
		retryMax: 5 * time.Millisecond,
		interval: time.Hour,
		logger:   slog.New(slog.NewTextHandler(&logs, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cleaner.Run(ctx) }()
	select {
	case <-succeeded:
	case <-time.After(time.Second):
		t.Fatal("cleaner did not retry a transient error")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleaner.Run error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("clean attempts = %d, want 2", calls.Load())
	}
	if !strings.Contains(logs.String(), "administrator authentication cleanup failed") ||
		!strings.Contains(logs.String(), "temporary database failure") {
		t.Fatalf("cleaner retry was not observable: %q", logs.String())
	}
}
