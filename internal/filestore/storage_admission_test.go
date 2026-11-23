package filestore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func admitWithin(t *testing.T, slots *storageSubjectSlots, ctx context.Context, wait time.Duration) (func(), error) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return slots.admit(waitCtx)
}

func TestStorageSubjectSlotsCapEachSubject(t *testing.T) {
	slots := &storageSubjectSlots{limit: 2}
	ctx := t.Context()
	greedy := make([]func(), 0, 2)
	for range 2 {
		release, err := slots.admit(WithStorageAdmissionSubject(ctx, "user:a"))
		if err != nil {
			t.Fatal(err)
		}
		greedy = append(greedy, release)
	}
	if _, err := admitWithin(t, slots, WithStorageAdmissionSubject(ctx, "user:a"), 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("subject over its share admitted: %v", err)
	}
	other, err := admitWithin(t, slots, WithStorageAdmissionSubject(ctx, "anonymous:2001:db8::/64"), time.Second)
	if err != nil {
		t.Fatalf("another subject waited behind the greedy one: %v", err)
	}
	other()

	waiting := make(chan error, 1)
	var queued func()
	go func() {
		var err error
		queued, err = slots.admit(WithStorageAdmissionSubject(ctx, "user:a"))
		waiting <- err
	}()
	select {
	case err := <-waiting:
		t.Fatalf("queued request admitted before a slot freed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	greedy[0]()
	if err := <-waiting; err != nil {
		t.Fatalf("queued request after release: %v", err)
	}
	queued()
	greedy[1]()
	if active := slots.active("user:a"); active != 0 {
		t.Fatalf("released subject holds %d slots", active)
	}
	if len(slots.entries) != 0 {
		t.Fatalf("idle subjects retained: %d", len(slots.entries))
	}
}

func TestStorageSubjectSlotsChargeARequestOnce(t *testing.T) {
	slots := &storageSubjectSlots{limit: 1}
	request := WithStorageAdmissionSubject(t.Context(), "service:token")
	first, err := slots.admit(request)
	if err != nil {
		t.Fatal(err)
	}
	// A ranged download holds its body while it opens the prefix: the second
	// operation of the same request must not wait for the request's own slot.
	second, err := admitWithin(t, slots, request, time.Second)
	if err != nil {
		t.Fatalf("nested operation of one request waited: %v", err)
	}
	if active := slots.active("service:token"); active != 1 {
		t.Fatalf("one request holds %d subject slots, want 1", active)
	}
	first()
	first() // Idempotent.
	if _, err := admitWithin(t, slots, WithStorageAdmissionSubject(t.Context(), "service:token"), 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slot freed while the request still had an operation: %v", err)
	}
	second()
	next, err := admitWithin(t, slots, WithStorageAdmissionSubject(t.Context(), "service:token"), time.Second)
	if err != nil {
		t.Fatalf("slot not freed after the request's last operation: %v", err)
	}
	next()
}

func TestStorageSubjectSlotsLeaveUnnamedCallersToTheSharedBound(t *testing.T) {
	slots := &storageSubjectSlots{limit: 1}
	for range 4 {
		if _, err := admitWithin(t, slots, t.Context(), time.Second); err != nil {
			t.Fatalf("unnamed caller limited: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(WithStorageAdmissionSubject(t.Context(), "user:b"))
	cancel()
	if _, err := slots.admit(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission = %v", err)
	}
	if len(slots.entries) != 0 {
		t.Fatalf("canceled admission retained an entry")
	}
}
