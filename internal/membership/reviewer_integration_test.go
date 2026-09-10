//go:build integration

package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestBackfillSkipsDelayedDualWriteInsideCutoverBound(t *testing.T) {
	historicalID := uuid.MustParse("0199b400-0000-7000-8000-000000000090")
	delayedID := uuid.MustParse("0199b400-0000-7000-8000-000000000010")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_delayed_dual_write",
		[]uuid.UUID{historicalID},
	)
	fixture.cutover(t)

	if err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := fixture.writer.LockCreate(fixture.ctx, tx, fixture.collectionID)
		if err != nil {
			return err
		}
		if err := fence.Reserve(fixture.ctx); err != nil {
			return err
		}
		if _, err := tx.Exec(fixture.ctx, `insert into app.membership_delayed_dual_write
			(id, created_at, updated_at) values ($1, $2, $2)`,
			delayedID,
			fixture.createdAt.Add(-time.Hour),
		); err != nil {
			return err
		}
		return fence.RecordCreated(fixture.ctx, delayedID)
	}); err != nil {
		t.Fatal(err)
	}

	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 1 || progress.Inserted != 1 || !progress.Ready || !progress.Done {
		t.Fatalf("backfill progress = %#v, want only the missing cutover row", progress)
	}

	ordinals := make(map[uuid.UUID]int64, 2)
	rows, err := fixture.pool.Query(fixture.ctx, `select record_id, ordinal
		from pgbase_meta.record_creation_ordinals where collection_id = $1`, fixture.collectionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var recordID uuid.UUID
		var ordinal int64
		if err := rows.Scan(&recordID, &ordinal); err != nil {
			t.Fatal(err)
		}
		ordinals[recordID] = ordinal
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ordinals) != 2 || ordinals[historicalID] != 1 || ordinals[delayedID] != 2 {
		t.Fatalf("membership ordinals = %#v, want historical=1 delayed=2", ordinals)
	}
}

func TestFinalBackfillValidationDoesNotHoldCreationFence(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_validation_create", nil)
	fixture.cutover(t)

	ordinalConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinalConn.Release()
	ordinalTx, err := ordinalConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(ordinalTx)
	if _, err := ordinalTx.Exec(fixture.ctx,
		`lock table pgbase_meta.record_creation_ordinals in access exclusive mode`); err != nil {
		t.Fatal(err)
	}

	backfillCtx, cancelBackfill := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancelBackfill()
	backfillResult := make(chan membershipConcurrencyResult[BackfillProgress], 1)
	go func() {
		progress, err := fixture.service.Backfill(backfillCtx)
		backfillResult <- membershipConcurrencyResult[BackfillProgress]{value: progress, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		0,
		"select count(*) from pgbase_meta.record_creation_ordinals",
	)

	createConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer createConn.Release()
	createTx, err := createConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(createTx)
	createCtx, cancelCreate := context.WithTimeout(fixture.ctx, 2*time.Second)
	defer cancelCreate()
	if _, err := fixture.writer.LockCreate(createCtx, createTx, fixture.collectionID); err != nil {
		t.Fatalf("create blocked behind final validation: %v", err)
	}

	if err := ordinalTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	progress := awaitMembershipConcurrencyResult(t, backfillResult)
	if !progress.Ready || !progress.Done {
		t.Fatalf("empty final backfill progress = %#v", progress)
	}
}

func TestReserveTimestampStartsAfterCreationFenceAcquisition(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_reserve_clock", nil)
	fixture.cutover(t)

	blockerConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blockerConn.Release()
	blockerTx, err := blockerConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(blockerTx)
	if _, err := blockerTx.Exec(fixture.ctx, `select collection_id
		from pgbase_meta.collection_creation_counters
		where collection_id = $1 for update`, fixture.collectionID); err != nil {
		t.Fatal(err)
	}

	createConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer createConn.Release()
	createTx, err := createConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(createTx)
	createPID := membershipConcurrencyBackendPID(t, createTx)
	lockCtx, cancelLock := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancelLock()
	lockResult := make(chan membershipConcurrencyResult[CreateFence], 1)
	go func() {
		fence, err := fixture.writer.LockCreate(lockCtx, createTx, fixture.collectionID)
		lockResult <- membershipConcurrencyResult[CreateFence]{value: fence, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		createPID,
		"join pgbase_meta.collection_creation_counters as creation_counter",
	)

	var releasedAfter time.Time
	if err := blockerTx.QueryRow(fixture.ctx, `select clock_timestamp()`).Scan(&releasedAfter); err != nil {
		t.Fatal(err)
	}
	if err := blockerTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	fence := awaitMembershipConcurrencyResult(t, lockResult)
	if err := fence.Reserve(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	var updatedAt time.Time
	if err := createTx.QueryRow(fixture.ctx, `select updated_at
		from pgbase_meta.collection_creation_counters where collection_id = $1`,
		fixture.collectionID,
	).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt.Before(releasedAfter) {
		t.Fatalf("counter updated_at %s predates lock release %s", updatedAt, releasedAfter)
	}
}

func TestMaintenanceRejectsIncompleteMembershipInstallationWithoutTransition(t *testing.T) {
	objects := []string{
		"record_membership_capability",
		"collection_creation_counters",
		"record_creation_ordinals",
		"record_membership_backfills",
		"runtime_writer_leases",
	}
	for _, object := range objects {
		t.Run(object, func(t *testing.T) {
			fixture := newMembershipConcurrencyFixture(t, "membership_incomplete_"+object, nil)
			var schemaVersionBefore, migrationsBefore int64
			if err := fixture.pool.QueryRow(fixture.ctx, `select
				(select version from pgbase_meta.schema_state where singleton),
				(select count(*) from pgbase_meta.system_migrations)`).Scan(
				&schemaVersionBefore,
				&migrationsBefore,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.pool.Exec(fixture.ctx,
				`drop table pgbase_meta.`+pgx.Identifier{object}.Sanitize()+` cascade`); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Status(fixture.ctx); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Status with missing %s error = %v, want ErrInvalidState", object, err)
			}
			if _, err := fixture.service.Cutover(fixture.ctx, FleetProof{
				Members: []FleetMember{fixture.runtime.FleetMember()},
			}); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Cutover with missing %s error = %v, want ErrInvalidState", object, err)
			}

			var schemaVersion, migrations int64
			if err := fixture.pool.QueryRow(fixture.ctx, `select
				(select version from pgbase_meta.schema_state where singleton),
				(select count(*) from pgbase_meta.system_migrations)`).Scan(
				&schemaVersion,
				&migrations,
			); err != nil {
				t.Fatal(err)
			}
			if schemaVersion != schemaVersionBefore || migrations != migrationsBefore {
				t.Fatalf(
					"schema/history changed after rejection = %d/%d, want %d/%d",
					schemaVersion,
					migrations,
					schemaVersionBefore,
					migrationsBefore,
				)
			}
			if object != "record_membership_capability" {
				var state State
				var generation int64
				if err := fixture.pool.QueryRow(fixture.ctx, `select state, generation
					from pgbase_meta.record_membership_capability where singleton`).Scan(
					&state,
					&generation,
				); err != nil {
					t.Fatal(err)
				}
				if state != StateOff || generation != 0 {
					t.Fatalf("capability changed after rejection = %s/%d", state, generation)
				}
			}
		})
	}
}

func TestCutoverReceiptReplayIgnoresLaterCollectionAddAndDelete(t *testing.T) {
	recordID := uuid.MustParse("0199b400-0000-7000-8000-000000000051")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_cutover_receipt",
		[]uuid.UUID{recordID},
	)
	want := fixture.cutover(t)
	addedID := uuid.MustParse("0199b400-0000-7000-8000-000000000052")
	insertPostTransitionMembershipCollection(t, fixture, addedID, "membership_cutover_added", false)

	got, err := fixture.service.Cutover(fixture.ctx, FleetProof{})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cutover replay after collection add = %#v, want %#v", got, want)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.collections
		where id = any($1::uuid[])`, []uuid.UUID{fixture.collectionID, addedID}); err != nil {
		t.Fatal(err)
	}
	got, err = fixture.service.Cutover(fixture.ctx, FleetProof{})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cutover replay after collection delete = %#v, want %#v", got, want)
	}
}

func TestReadyReceiptReplayIgnoresLaterCollectionAddAndDelete(t *testing.T) {
	recordID := uuid.MustParse("0199b400-0000-7000-8000-000000000061")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_ready_receipt",
		[]uuid.UUID{recordID},
	)
	fixture.cutover(t)
	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Done {
		t.Fatalf("ready receipt backfill = %#v", progress)
	}
	want, err := fixture.service.PromoteReady(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	addedID := uuid.MustParse("0199b400-0000-7000-8000-000000000062")
	insertPostTransitionMembershipCollection(t, fixture, addedID, "membership_ready_added", true)

	got, err := fixture.service.PromoteReady(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ready replay after collection add = %#v, want %#v", got, want)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.collections
		where id = any($1::uuid[])`, []uuid.UUID{fixture.collectionID, addedID}); err != nil {
		t.Fatal(err)
	}
	got, err = fixture.service.PromoteReady(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ready replay after collection delete = %#v, want %#v", got, want)
	}
}

func TestCutoverExclusiveSchemaBarrierWaitsForLegacyTransaction(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_legacy_barrier", nil)
	legacyConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyConn.Release()
	legacyTx, err := legacyConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(legacyTx)
	if _, err := legacyTx.Exec(fixture.ctx, `select version
		from pgbase_meta.schema_state where singleton for share`); err != nil {
		t.Fatal(err)
	}

	cutoverCtx, cancelCutover := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancelCutover()
	cutoverResult := make(chan membershipConcurrencyResult[CutoverReceipt], 1)
	go func() {
		receipt, err := fixture.service.Cutover(cutoverCtx, FleetProof{
			Members: []FleetMember{fixture.runtime.FleetMember()},
		})
		cutoverResult <- membershipConcurrencyResult[CutoverReceipt]{value: receipt, err: err}
	}()
	waitForMembershipPostgresLock(t, fixture.pool, 0, "from pgbase_meta.schema_state")

	var state State
	var generation int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select state, generation
		from pgbase_meta.record_membership_capability where singleton`).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != StateOff || generation != 0 {
		t.Fatalf("capability crossed barrier while legacy tx was open: %s/%d", state, generation)
	}
	if err := legacyTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	receipt := awaitMembershipConcurrencyResult(t, cutoverResult)
	if receipt.Generation != 1 || receipt.Collections != 1 || receipt.Records != 0 {
		t.Fatalf("cutover after legacy drain = %#v", receipt)
	}
}

func TestResolvedTraversalOwnsReadyGenerationAndHighWaterChecks(t *testing.T) {
	recordID := uuid.MustParse("0199b400-0000-7000-8000-000000000081")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_validate_traversal",
		[]uuid.UUID{recordID},
	)
	fixture.cutover(t)
	if progress, err := fixture.service.Backfill(fixture.ctx); err != nil || !progress.Done {
		t.Fatalf("backfill before traversal validation = %#v/%v", progress, err)
	}
	if _, err := fixture.service.PromoteReady(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	testutil.RetireRecordMembershipCapability(t, fixture.ctx, fixture.pool)

	if err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		traversal, err := ResolveTraversal(fixture.ctx, tx, fixture.collectionID)
		if err != nil {
			return err
		}
		if err := traversal.ValidateV5(1, 0); err != nil {
			return err
		}
		return traversal.ValidateV5(1, 1)
	}); err != nil {
		t.Fatalf("valid traversal rejected: %v", err)
	}

	invalid := []struct {
		name       string
		generation int64
		highWater  int64
	}{
		{name: "zero generation", generation: 0, highWater: 0},
		{name: "wrong generation", generation: 2, highWater: 1},
		{name: "negative high water", generation: 1, highWater: -1},
		{name: "future high water", generation: 1, highWater: 2},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
				traversal, resolveErr := ResolveTraversal(fixture.ctx, tx, fixture.collectionID)
				if resolveErr != nil {
					return resolveErr
				}
				return traversal.ValidateV5(test.generation, test.highWater)
			})
			if !errors.Is(err, ErrTraversalInvalid) {
				t.Fatalf("ValidateV5 error = %v, want ErrTraversalInvalid", err)
			}
		})
	}
}

func TestResolveTraversalFailsClosedBeforeReadyAndOnPartialInstallation(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_traversal_fail_closed", nil)
	fixture.cutover(t)
	// Dual-write no longer reaches ValidateV5 at all: the resolver refuses a
	// boundary that has not retired v4, because this binary has no cursor it
	// could issue for one.
	if err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, resolveErr := ResolveTraversal(fixture.ctx, tx, fixture.collectionID)
		return resolveErr
	}); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTraversalInvalid) {
		t.Fatalf("dual-write ResolveTraversal error = %v, want ErrUnavailable", err)
	}
	assertTraversalUnavailable := func(t *testing.T) {
		t.Helper()
		err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, resolveErr := ResolveTraversal(fixture.ctx, tx, fixture.collectionID)
			return resolveErr
		})
		if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTraversalInvalid) {
			t.Fatalf("ResolveTraversal error = %v, want ErrUnavailable", err)
		}
	}
	if _, err := fixture.pool.Exec(fixture.ctx,
		`drop table pgbase_meta.record_creation_ordinals cascade`); err != nil {
		t.Fatal(err)
	}
	assertTraversalUnavailable(t)
}

func insertPostTransitionMembershipCollection(
	t *testing.T,
	fixture membershipConcurrencyFixture,
	collectionID uuid.UUID,
	name string,
	ready bool,
) {
	t.Helper()
	if _, err := fixture.pool.Exec(fixture.ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $2, 'active', $3, $3)`, collectionID, name, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 1, 0, $2)`,
		collectionID,
		fixture.createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if ready {
		if _, err := fixture.pool.Exec(fixture.ctx, `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, cutover_count, last_assigned_ordinal,
			 validated_base_count, validated_sidecar_count, updated_at, completed_at)
			values ($1, 1, 'ready', 0, 0, 0, 0, $2, $2)`, collectionID, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, cutover_count, last_assigned_ordinal, updated_at)
		values ($1, 1, 'backfilling', 0, 0, $2)`, collectionID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
}
