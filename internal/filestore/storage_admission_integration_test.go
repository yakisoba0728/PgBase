//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// One subject's requests cannot take every shared storage slot: past its
// share they wait outside the shared queue, and another subject is admitted
// to the slots that remain.
func TestStorageOperationsKeepSlotsForOtherSubjects(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{9}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	controller.operations = make(chan struct{}, 4)
	controller.subjects.limit = 2

	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for range 2 {
		_, release, err := controller.BeginStorageOperation(WithStorageAdmissionSubject(ctx, "user:greedy"))
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	waitCtx, stop := context.WithTimeout(WithStorageAdmissionSubject(ctx, "user:greedy"), 50*time.Millisecond)
	_, extra, err := controller.BeginStorageOperation(waitCtx)
	stop()
	if extra != nil {
		extra()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("subject over its share = %v, want to wait", err)
	}
	if active := len(controller.operations); active != 2 {
		t.Fatalf("waiting subject holds shared slots: %d active, want 2", active)
	}
	for _, subject := range []string{"user:other", "anonymous:192.0.2.1"} {
		_, release, err := controller.BeginStorageOperation(WithStorageAdmissionSubject(ctx, subject))
		if err != nil {
			t.Fatalf("%s behind a greedy subject: %v", subject, err)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
	releases = nil
	if active := len(controller.operations); active != 0 {
		t.Fatalf("released operations retained %d shared slots", active)
	}
	if active := controller.subjects.active("user:greedy"); active != 0 {
		t.Fatalf("released operations retained %d subject slots", active)
	}
}

// Downloads borrow reusable coordination sessions. Returning a session for
// reuse, or closing one that cancellation may have closed, must give back the
// shared slot and the caller's slot either way, and a request that opens a
// second read while holding the first (a ranged variant reads its prefix) is
// charged once.
func TestReusableReadSessionsReleaseCallerAndSharedSlots(t *testing.T) {
	_, controller, key := openReadReuseFixture(t)
	controller.subjects.limit = 1
	ctx := t.Context()
	requireIdle := func(step string) {
		t.Helper()
		if active := len(controller.operations); active != 0 {
			t.Fatalf("%s: %d shared slots held, want 0", step, active)
		}
		if active := controller.subjects.active("user:reader"); active != 0 {
			t.Fatalf("%s: %d caller slots held, want 0", step, active)
		}
	}

	for iteration := range 3 {
		reader, err := controller.Open(WithStorageAdmissionSubject(ctx, "user:reader"), key)
		if err != nil {
			t.Fatal(err)
		}
		if active := controller.subjects.active("user:reader"); active != 1 {
			t.Fatalf("iteration %d: open read holds %d caller slots, want 1", iteration, active)
		}
		// The caller's only slot is taken, so its next request waits.
		waitCtx, stop := context.WithTimeout(WithStorageAdmissionSubject(ctx, "user:reader"), 50*time.Millisecond)
		_, err = controller.Open(waitCtx, key)
		stop()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("iteration %d: second request over the caller's share = %v", iteration, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		requireIdle("returned session")
	}

	request := WithStorageAdmissionSubject(ctx, "user:reader")
	body, err := controller.OpenRange(request, key, ByteRange{Start: 10, End: 19})
	if err != nil {
		t.Fatal(err)
	}
	nestedCtx, stop := context.WithTimeout(request, time.Second)
	prefix, err := controller.OpenRange(nestedCtx, key, ByteRange{Start: 0, End: 9})
	stop()
	if err != nil {
		t.Fatalf("second read of one request waited for its own slot: %v", err)
	}
	if active := controller.subjects.active("user:reader"); active != 1 {
		t.Fatalf("one request holds %d caller slots, want 1", active)
	}
	_ = prefix.Body.Close()
	_ = body.Body.Close()
	requireIdle("nested reads")

	for iteration := range 20 {
		readCtx, cancel := context.WithCancel(WithStorageAdmissionSubject(ctx, "user:reader"))
		reader, err := controller.Open(readCtx, key)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if iteration%2 == 0 {
			cancel()
		}
		_ = reader.Close()
		cancel()
		requireIdle("canceled read")
	}
	reader, err := controller.Open(WithStorageAdmissionSubject(ctx, "user:reader"), key)
	if err != nil {
		t.Fatalf("read after canceled reads: %v", err)
	}
	_ = reader.Close()
	requireIdle("final read")
}
