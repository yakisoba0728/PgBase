//go:build integration

package filestore

import (
	"errors"
	"testing"
	"time"
)

// Concurrent requests for a variant of one file queue on the file row lock
// held by the render in progress. A waiter that gives up must report
// ErrImageBusy (503 file_busy), whichever timeout ends its wait, instead of
// the raw PostgreSQL error that used to surface as 500 file_error.
func TestConcurrentVariantRequestsReportBusy(t *testing.T) {
	for _, test := range []struct {
		name     string
		lockWait time.Duration
	}{
		// newClosureFile's pool has a one-second statement_timeout.
		{name: "statement timeout", lockWait: 30 * time.Second},
		{name: "lock timeout", lockWait: 100 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := lockedImageBudgetWait
			lockedImageBudgetWait = test.lockWait
			t.Cleanup(func() { lockedImageBudgetWait = previous })
			_, service, file, _, store := newClosureFile(t, "attachment")
			store.entered, store.resume = make(chan struct{}), make(chan struct{})
			type outcome struct {
				key string
				err error
			}
			first := make(chan outcome, 1)
			go func() {
				key, err := service.variantObject(t.Context(), file, "2x2")
				first <- outcome{key, err}
			}()
			<-store.entered
			// Later renders must not block in Open; they should never get there.
			store.entered = nil
			const waiters = 4
			errs := make(chan error, waiters)
			for range waiters {
				go func() {
					_, err := service.variantObject(t.Context(), file, "2x2")
					errs <- err
				}()
			}
			for range waiters {
				if err := <-errs; !errors.Is(err, ErrImageBusy) {
					close(store.resume)
					t.Fatalf("concurrent variant err=%v, want ErrImageBusy", err)
				}
			}
			close(store.resume)
			rendered := <-first
			if rendered.err != nil {
				t.Fatalf("holding render failed: %v", rendered.err)
			}
			key, err := service.variantObject(t.Context(), file, "2x2")
			if err != nil || key != rendered.key {
				t.Fatalf("retry key=%q err=%v, want published %q", key, err, rendered.key)
			}
		})
	}
}
