package mailer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type queueKey struct{}

func TestQueueRunsJobsDetachedAndBounded(t *testing.T) {
	queue := NewQueue(1, 2, time.Second)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var ran atomic.Int32
	requestCtx, cancel := context.WithCancel(context.WithValue(context.Background(), queueKey{}, "request"))
	job := func(ctx context.Context) {
		if ctx.Value(queueKey{}) != "request" {
			t.Error("job lost the request context values")
		}
		started <- struct{}{}
		<-release
		if ctx.Err() != nil {
			t.Error("job was cancelled with its request")
		}
		ran.Add(1)
	}
	if !queue.Go(requestCtx, job) || !queue.Go(requestCtx, job) {
		t.Fatal("queue refused jobs within its capacity")
	}
	cancel()
	if queue.Go(context.Background(), job) {
		t.Fatal("queue accepted a job beyond its capacity")
	}
	<-started
	select {
	case <-started:
		t.Fatal("queue ran more jobs at once than its workers")
	case <-time.After(50 * time.Millisecond):
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if err := queue.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait with unfinished jobs = %v", err)
	}
	close(release)
	if err := queue.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 2 {
		t.Fatalf("ran %d jobs, want 2", ran.Load())
	}
	if queue.Go(context.Background(), job) {
		t.Fatal("closed queue accepted a job")
	}
}

func TestQueueContainsPanicsAndTimesOutJobs(t *testing.T) {
	queue := NewQueue(2, 2, 20*time.Millisecond)
	queue.Go(context.Background(), func(context.Context) { panic("job failure") })
	expired := make(chan error, 1)
	queue.Go(context.Background(), func(ctx context.Context) {
		<-ctx.Done()
		expired <- ctx.Err()
	})
	if err := queue.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-expired; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("job context error = %v, want its timeout", err)
	}
	// The queue is idle again and accepts new work.
	done := make(chan struct{})
	if !queue.Go(context.Background(), func(context.Context) { close(done) }) {
		t.Fatal("queue refused a job after the earlier ones finished")
	}
	<-done
}
