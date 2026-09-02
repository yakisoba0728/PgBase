//go:build integration

package membership

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestOffLockCreateFencesCoexist(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_off_shared_fences", nil)
	firstConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstConnection.Release()
	firstTx, err := firstConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(firstTx)
	first, err := fixture.writer.LockCreate(fixture.ctx, firstTx, fixture.collectionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.state != StateOff || !first.counterLocked {
		t.Fatalf("first off create fence = %#v, want locked off fence", first)
	}

	secondConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer secondConnection.Release()
	secondTx, err := secondConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(secondTx)
	secondCtx, cancelSecond := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancelSecond()
	secondResult := make(chan membershipConcurrencyResult[CreateFence], 1)
	go func() {
		fence, err := fixture.writer.LockCreate(secondCtx, secondTx, fixture.collectionID)
		secondResult <- membershipConcurrencyResult[CreateFence]{value: fence, err: err}
	}()
	select {
	case completed := <-secondResult:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		if completed.value.state != StateOff || !completed.value.counterLocked {
			t.Fatalf("second off create fence = %#v, want locked off fence", completed.value)
		}
	case <-secondCtx.Done():
		if err := firstTx.Rollback(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		completed := awaitRawMembershipConcurrencyResult(t, secondResult)
		if completed.err != nil {
			t.Fatalf("second off create fence after release: %v", completed.err)
		}
		t.Fatal("second off LockCreate waited behind the first counter fence")
	}
}

func TestCreateKeyShareFenceBlocksCutoverCounterUpdate(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_key_share_cutover", nil)
	createConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer createConnection.Release()
	createTx, err := createConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(createTx)
	if _, err := fixture.writer.LockCreate(fixture.ctx, createTx, fixture.collectionID); err != nil {
		t.Fatal(err)
	}

	cutoverConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cutoverConnection.Release()
	cutoverTx, err := cutoverConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(cutoverTx)
	cutoverPID := membershipConcurrencyBackendPID(t, cutoverTx)
	cutoverCtx, cancelCutover := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelCutover()
	// Exercise the real phase-two counter lock directly. Public Cutover first
	// waits at its schema barrier, which would mask this row-lock conflict.
	cutoverResult := make(chan membershipConcurrencyResult[[]cutoverCollection], 1)
	go func() {
		collections, err := lockCutoverCollections(cutoverCtx, cutoverTx)
		cutoverResult <- membershipConcurrencyResult[[]cutoverCollection]{value: collections, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		cutoverPID,
		"from pgbase_meta.collection_creation_counters as counters",
	)
	if err := createTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	collections := awaitMembershipConcurrencyResult(t, cutoverResult)
	if len(collections) != 1 || collections[0].id != fixture.collectionID {
		t.Fatalf("cutover-locked collections = %#v, want fixture collection", collections)
	}
}

func TestSingleStatementLockCreateLocksCounterBeforeCapability(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_counter_before_capability", nil)
	capabilityConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilityConnection.Release()
	capabilityTx, err := capabilityConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(capabilityTx)
	var singleton bool
	if err := capabilityTx.QueryRow(fixture.ctx, `select singleton
		from pgbase_meta.record_membership_capability where singleton for update`).Scan(&singleton); err != nil {
		t.Fatal(err)
	}

	writerConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writerConnection.Release()
	writerTx, err := writerConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(writerTx)
	writerPID := membershipConcurrencyBackendPID(t, writerTx)
	writerCtx, cancelWriter := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelWriter()
	writerResult := make(chan membershipConcurrencyResult[CreateFence], 1)
	go func() {
		fence, err := fixture.writer.LockCreate(writerCtx, writerTx, fixture.collectionID)
		writerResult <- membershipConcurrencyResult[CreateFence]{value: fence, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		writerPID,
		"with pinned_schema as materialized",
	)

	cutoverConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cutoverConnection.Release()
	cutoverTx, err := cutoverConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(cutoverTx)
	cutoverPID := membershipConcurrencyBackendPID(t, cutoverTx)
	cutoverResult := make(chan error, 1)
	go func() {
		var collectionID uuid.UUID
		cutoverResult <- cutoverTx.QueryRow(writerCtx, `select collection_id
			from pgbase_meta.collection_creation_counters
			where collection_id = $1 for update`, fixture.collectionID).Scan(&collectionID)
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		cutoverPID,
		"from pgbase_meta.collection_creation_counters",
	)

	if err := capabilityTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	fence := awaitMembershipConcurrencyResult(t, writerResult)
	if fence.state != StateOff || !fence.counterLocked {
		t.Fatalf("single-statement create fence = %#v, want locked off fence", fence)
	}
	if err := writerTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if err := awaitMembershipConcurrencyError(t, cutoverResult); err != nil {
		t.Fatalf("cutover counter lock after create release: %v", err)
	}
}

func TestCreateKeyShareFencesSerializeReservationsWithoutGaps(t *testing.T) {
	states := []State{StateDualWrite, StateReady}
	releases := []struct {
		name     string
		rollback bool
	}{
		{name: "commit"},
		{name: "rollback", rollback: true},
	}
	for _, state := range states {
		for _, release := range releases {
			t.Run(string(state)+"/"+release.name, func(t *testing.T) {
				fixture := newMembershipConcurrencyFixture(t, "membership_serial_reserve", nil)
				fixture.cutover(t)
				if state == StateReady {
					progress, err := fixture.service.Backfill(fixture.ctx)
					if err != nil {
						t.Fatal(err)
					}
					if !progress.Ready || !progress.Done {
						t.Fatalf("empty reservation backfill = %#v, want ready", progress)
					}
					if _, err := fixture.service.PromoteReady(fixture.ctx); err != nil {
						t.Fatal(err)
					}
				}

				firstConnection, err := fixture.pool.Acquire(fixture.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer firstConnection.Release()
				firstTx, err := firstConnection.Begin(fixture.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rollbackMembershipConcurrencyTx(firstTx)
				firstFence, err := fixture.writer.LockCreate(fixture.ctx, firstTx, fixture.collectionID)
				if err != nil {
					t.Fatal(err)
				}

				secondConnection, err := fixture.pool.Acquire(fixture.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer secondConnection.Release()
				secondTx, err := secondConnection.Begin(fixture.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rollbackMembershipConcurrencyTx(secondTx)
				secondPID := membershipConcurrencyBackendPID(t, secondTx)
				secondLockCtx, cancelSecondLock := context.WithTimeout(fixture.ctx, 10*time.Second)
				defer cancelSecondLock()
				secondFence, err := fixture.writer.LockCreate(
					secondLockCtx,
					secondTx,
					fixture.collectionID,
				)
				if err != nil {
					t.Fatalf("second %s LockCreate did not coexist with first: %v", state, err)
				}

				if err := firstFence.Reserve(fixture.ctx); err != nil {
					t.Fatalf("first %s Reserve with second key-share fence: %v", state, err)
				}
				firstRecordID := uuid.MustParse("0199b600-0000-7000-8000-000000000011")
				insertReservedMembershipRecord(t, fixture, firstTx, firstFence, firstRecordID)

				reserveCtx, cancelReserve := context.WithTimeout(fixture.ctx, 10*time.Second)
				defer cancelReserve()
				secondReserve := make(chan error, 1)
				go func() { secondReserve <- secondFence.Reserve(reserveCtx) }()
				waitForMembershipPostgresLock(
					t,
					fixture.pool,
					secondPID,
					"update pgbase_meta.collection_creation_counters",
				)

				if release.rollback {
					if err := firstTx.Rollback(fixture.ctx); err != nil {
						t.Fatal(err)
					}
				} else if err := firstTx.Commit(fixture.ctx); err != nil {
					t.Fatal(err)
				}
				if err := awaitMembershipConcurrencyError(t, secondReserve); err != nil {
					t.Fatalf("second %s Reserve after first %s: %v", state, release.name, err)
				}
				secondRecordID := uuid.MustParse("0199b600-0000-7000-8000-000000000012")
				insertReservedMembershipRecord(t, fixture, secondTx, secondFence, secondRecordID)
				if err := secondTx.Commit(fixture.ctx); err != nil {
					t.Fatal(err)
				}

				want := []int64{1, 2}
				if release.rollback {
					want = []int64{1}
				}
				assertMembershipOrdinals(t, fixture, want)
			})
		}
	}
}

func insertReservedMembershipRecord(
	t *testing.T,
	fixture membershipConcurrencyFixture,
	tx pgx.Tx,
	fence CreateFence,
	recordID uuid.UUID,
) {
	t.Helper()
	table := pgx.Identifier{"app", fixture.tableName}.Sanitize()
	if _, err := tx.Exec(fixture.ctx, `insert into `+table+`
		(id, created_at, updated_at) values ($1, $2, $2)`, recordID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if err := fence.RecordCreated(fixture.ctx, recordID); err != nil {
		t.Fatal(err)
	}
}

func assertMembershipOrdinals(
	t *testing.T,
	fixture membershipConcurrencyFixture,
	want []int64,
) {
	t.Helper()
	var lastOrdinal int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select last_ordinal
		from pgbase_meta.collection_creation_counters where collection_id = $1`,
		fixture.collectionID,
	).Scan(&lastOrdinal); err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.pool.Query(fixture.ctx, `select ordinal
		from pgbase_meta.record_creation_ordinals
		where collection_id = $1 order by ordinal`, fixture.collectionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make([]int64, 0, len(want))
	for rows.Next() {
		var ordinal int64
		if err := rows.Scan(&ordinal); err != nil {
			t.Fatal(err)
		}
		got = append(got, ordinal)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if lastOrdinal != int64(len(want)) || len(got) != len(want) {
		t.Fatalf("last ordinal/sidecars = %d/%v, want %d/%v", lastOrdinal, got, len(want), want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("sidecar ordinals = %v, want %v", got, want)
		}
	}
}
