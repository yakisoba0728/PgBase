//go:build integration

package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// A schema apply holds pgbase_meta.schema_state FOR UPDATE for the whole of its
// DDL with the request statement timeout disabled, while the lease heartbeat
// runs under the pool's ordinary timeout. Pinning the same row from the
// heartbeat therefore made an ordinary large ALTER expire the very lease the
// pin was supposed to protect, and that failure took the whole app down.
func TestHeartbeatRenewsWhileALongSchemaDDLHoldsTheExclusivePin(t *testing.T) {
	fixture := newBootstrapFixture(t)
	runtime, err := RegisterWriter(fixture.ctx, fixture.service, WriterC)
	if err != nil {
		t.Fatal(err)
	}
	runtime.heartbeatInterval = 200 * time.Millisecond
	before := leaseHeartbeatAt(t, fixture, runtime.instanceID)

	tx := beginPinnedDDL(t, fixture)
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	// Longer than the pool's five second statement timeout, which is where the
	// pinned heartbeat died with SQLSTATE 57014.
	renewed := false
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); {
		select {
		case runErr := <-done:
			t.Fatalf("membership writer stopped under a schema DDL with its lease still valid: %v", runErr)
		case <-time.After(250 * time.Millisecond):
		}
		if leaseHeartbeatAt(t, fixture, runtime.instanceID).After(before) {
			renewed = true
		}
	}
	if !renewed {
		t.Fatal("lease was never renewed while the schema DDL held the exclusive schema pin")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
}

// Renewal still has to reach the capability row, which every rollout transition
// locks exclusively. Losing renewals to that is ordinary while the lease this
// process already holds is safely in the future.
func TestHeartbeatToleratesCapabilityContentionWhileTheLeaseIsSafe(t *testing.T) {
	fixture := newBootstrapFixture(t)
	runtime, pool := impatientRuntime(t, fixture)
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if err := waitForRenewal(fixture, pool, runtime.instanceID); err != nil {
		t.Fatalf("running membership writer never renewed its lease: %v", err)
	}

	release := lockCapabilityExclusively(t, fixture)
	select {
	case runErr := <-done:
		t.Fatalf("membership writer stopped on contention with a lease still good for %s: %v",
			runtime.leaseTolerance, runErr)
	case <-time.After(2 * time.Second):
	}

	release()
	if err := waitForRenewal(fixture, pool, runtime.instanceID); err != nil {
		t.Fatalf("lease was not renewed once the capability row was released: %v", err)
	}
	cancel()
	<-done
}

// The tolerance is what stops a heartbeat that cannot renew from outliving its
// own lease. Once it is spent the loop must stop, whatever the failure was.
func TestHeartbeatStopsWhenTheLeaseToleranceIsSpent(t *testing.T) {
	fixture := newBootstrapFixture(t)
	runtime, pool := impatientRuntime(t, fixture)
	// Set before the loop reads it: every renewal is then already outside the
	// window, so the first one that cannot reach the database has to stop.
	runtime.leaseTolerance = time.Nanosecond

	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if err := waitForRenewal(fixture, pool, runtime.instanceID); err != nil {
		t.Fatalf("running membership writer never renewed its lease: %v", err)
	}
	lockCapabilityExclusively(t, fixture)
	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("membership writer reported a clean stop after its lease tolerance ran out")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("membership writer kept renewing past the end of its lease tolerance")
	}
}

// A writer the fleet has moved past must stop at once, however much lease time
// is left: tolerance is for contention, never for a refusal.
func TestHeartbeatStopsWhenTheFleetMinimumPassesThisWriter(t *testing.T) {
	fixture := newBootstrapFixture(t)
	runtime, err := RegisterWriter(fixture.ctx, fixture.service, WriterA)
	if err != nil {
		t.Fatal(err)
	}
	runtime.heartbeatInterval = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if err := waitForRenewal(fixture, fixture.pool, runtime.instanceID); err != nil {
		t.Fatalf("running membership writer never renewed its lease: %v", err)
	}
	// The terminal state the rollout leaves behind, which fixes the minimum
	// writer version at Release B and so retires this Release A writer.
	if _, err := fixture.pool.Exec(
		fixture.ctx, bootstrapCapabilityUpdate, time.Now().UTC(), WriterB, 1,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case runErr := <-done:
		if !errors.Is(runErr, ErrWriterTooOld) {
			t.Fatalf("membership writer stop reason = %v, want %v", runErr, ErrWriterTooOld)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("membership writer kept its lease after the fleet minimum passed it")
	}
}

// beginPinnedDDL reproduces the lock profile of a long schema apply: the
// exclusive schema pin, the request statement timeout disabled, and real DDL,
// all held open by the returned transaction.
func beginPinnedDDL(t *testing.T, fixture bootstrapFixture) pgx.Tx {
	t.Helper()
	connection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Release)
	tx, err := connection.BeginTx(fixture.ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(fixture.ctx, "set local statement_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	if err := pinSchemaExclusive(fixture.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(fixture.ctx, `alter table app.bootstrap_records
		add column long_running_ddl_marker text`); err != nil {
		t.Fatal(err)
	}
	return tx
}

// impatientRuntime registers a writer over a pool whose statement timeout is
// short, so blocked renewal surfaces in test time rather than in the five
// seconds a deployed request budget allows.
func impatientRuntime(t *testing.T, fixture bootstrapFixture) (*Runtime, *database.Pool) {
	t.Helper()
	pool, err := database.Open(fixture.ctx, fixture.databaseURL, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runtime, err := RegisterWriter(fixture.ctx, New(pool), WriterC)
	if err != nil {
		t.Fatal(err)
	}
	runtime.heartbeatInterval = 100 * time.Millisecond
	return runtime, pool
}

// lockCapabilityExclusively holds the row every rollout transition takes FOR
// UPDATE. The returned release is safe to call more than once.
func lockCapabilityExclusively(t *testing.T, fixture bootstrapFixture) func() {
	t.Helper()
	connection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := connection.BeginTx(fixture.ctx, pgx.TxOptions{})
	if err != nil {
		connection.Release()
		t.Fatal(err)
	}
	if _, err := statusForUpdate(fixture.ctx, tx); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = tx.Rollback(context.Background())
		connection.Release()
	}
	t.Cleanup(release)
	return release
}

func waitForRenewal(fixture bootstrapFixture, pool *database.Pool, instanceID uuid.UUID) error {
	before, err := leaseHeartbeat(fixture, pool, instanceID)
	if err != nil {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		latest, err := leaseHeartbeat(fixture, pool, instanceID)
		if err != nil {
			return err
		}
		if latest.After(before) {
			return nil
		}
	}
	return errors.New("lease heartbeat never advanced")
}

func leaseHeartbeatAt(t *testing.T, fixture bootstrapFixture, instanceID uuid.UUID) time.Time {
	t.Helper()
	heartbeatAt, err := leaseHeartbeat(fixture, fixture.pool, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	return heartbeatAt
}

func leaseHeartbeat(
	fixture bootstrapFixture,
	pool *database.Pool,
	instanceID uuid.UUID,
) (time.Time, error) {
	var heartbeatAt time.Time
	err := pool.QueryRow(fixture.ctx, `select heartbeat_at
		from pgbase_meta.runtime_writer_leases where instance_id = $1`, instanceID).Scan(&heartbeatAt)
	return heartbeatAt, err
}
