//go:build integration

package membership

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestCreateFenceHeldAcrossCutoverIncludesCommittedRecord(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_create_cutover", nil)
	recordID := uuid.MustParse("0199b300-0000-7000-8000-000000000011")

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

	createFence, err := fixture.writer.LockCreate(fixture.ctx, createTx, fixture.collectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := createFence.Reserve(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := createTx.Exec(fixture.ctx, `insert into app.membership_create_cutover
		(id, created_at, updated_at) values ($1, $2, $2)`, recordID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if err := createFence.RecordCreated(fixture.ctx, recordID); err != nil {
		t.Fatal(err)
	}

	cutoverCtx, cancelCutover := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelCutover()
	cutoverResult := make(chan membershipConcurrencyResult[CutoverReceipt], 1)
	go func() {
		receipt, err := fixture.service.Cutover(cutoverCtx, FleetProof{
			Members: []FleetMember{fixture.runtime.FleetMember()},
		})
		cutoverResult <- membershipConcurrencyResult[CutoverReceipt]{value: receipt, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		0,
		"from pgbase_meta.schema_state",
	)

	if err := createTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	receipt := awaitMembershipConcurrencyResult(t, cutoverResult)
	if receipt.Generation != 1 || receipt.Collections != 1 || receipt.Records != 1 {
		t.Fatalf("cutover receipt = %#v, want one committed record", receipt)
	}

	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Ready || !progress.Done || progress.Inserted != 1 {
		t.Fatalf("backfill progress = %#v, want included record completed", progress)
	}
	var ordinal int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select ordinal
		from pgbase_meta.record_creation_ordinals
		where collection_id = $1 and record_id = $2`, fixture.collectionID, recordID).Scan(&ordinal); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("included record ordinal = %d, want 1", ordinal)
	}
}

func TestBackfillBaseLockThenDeleteLeavesNoMembershipOrphan(t *testing.T) {
	recordID := uuid.MustParse("0199b300-0000-7000-8000-000000000021")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_backfill_first",
		[]uuid.UUID{recordID},
	)
	fixture.cutover(t)

	sidecarConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sidecarConn.Release()
	sidecarTx, err := sidecarConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(sidecarTx)
	dummyID := uuid.MustParse("0199b300-0000-7000-8000-000000000022")
	if _, err := sidecarTx.Exec(fixture.ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, 1)`, fixture.collectionID, dummyID); err != nil {
		t.Fatal(err)
	}

	backfillCtx, cancelBackfill := context.WithTimeout(fixture.ctx, 10*time.Second)
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
		"insert into pgbase_meta.record_creation_ordinals",
	)

	deleteConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteConn.Release()
	deleteTx, err := deleteConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(deleteTx)
	deleteFence, err := fixture.writer.Begin(fixture.ctx, deleteTx)
	if err != nil {
		t.Fatal(err)
	}
	deletePID := membershipConcurrencyBackendPID(t, deleteTx)
	deleteResult := make(chan error, 1)
	go func() {
		tag, err := deleteTx.Exec(backfillCtx,
			`delete from app.membership_backfill_first where id = $1`, recordID)
		if err == nil && tag.RowsAffected() != 1 {
			err = fmt.Errorf("delete base record affected %d rows, want 1", tag.RowsAffected())
		}
		if err == nil {
			err = deleteFence.Delete(backfillCtx, []Record{{
				CollectionID: fixture.collectionID,
				RecordID:     recordID,
			}})
		}
		if err == nil {
			err = deleteTx.Commit(backfillCtx)
		}
		deleteResult <- err
	}()
	waitForMembershipPostgresLock(t, fixture.pool, deletePID, "delete from app.membership_backfill_first")

	if err := sidecarTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	progress := awaitMembershipConcurrencyResult(t, backfillResult)
	if !progress.Ready || !progress.Done || progress.Inserted != 1 {
		t.Fatalf("backfill progress = %#v, want committed base-first batch", progress)
	}
	if err := awaitMembershipConcurrencyError(t, deleteResult); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoBaseOrSidecar(t, recordID)
}

func TestDeleteBaseLockThenBackfillDoesNotResurrectMembership(t *testing.T) {
	recordID := uuid.MustParse("0199b300-0000-7000-8000-000000000031")
	fixture := newMembershipConcurrencyFixture(
		t,
		"membership_delete_first",
		[]uuid.UUID{recordID},
	)
	fixture.cutover(t)

	deleteConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteConn.Release()
	deleteTx, err := deleteConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(deleteTx)
	deleteFence, err := fixture.writer.Begin(fixture.ctx, deleteTx)
	if err != nil {
		t.Fatal(err)
	}
	if tag, err := deleteTx.Exec(fixture.ctx,
		`delete from app.membership_delete_first where id = $1`, recordID); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("delete base record affected %d rows, want 1", tag.RowsAffected())
	}

	backfillCtx, cancelBackfill := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelBackfill()
	backfillResult := make(chan membershipConcurrencyResult[BackfillProgress], 1)
	go func() {
		progress, err := fixture.service.Backfill(backfillCtx)
		backfillResult <- membershipConcurrencyResult[BackfillProgress]{value: progress, err: err}
	}()
	waitForMembershipPostgresLock(t, fixture.pool, 0, "membership_delete_first")

	if err := deleteFence.Delete(fixture.ctx, []Record{{
		CollectionID: fixture.collectionID,
		RecordID:     recordID,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := deleteTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	progress := awaitMembershipConcurrencyResult(t, backfillResult)
	if !progress.Ready || !progress.Done || progress.Inserted != 0 {
		t.Fatalf("backfill progress = %#v, want deleted row skipped", progress)
	}
	fixture.assertNoBaseOrSidecar(t, recordID)
}

func TestBackfillWorkerInterruptionRestartsFromDurableCheckpoint(t *testing.T) {
	recordIDs := []uuid.UUID{
		uuid.MustParse("0199b300-0000-7000-8000-000000000041"),
		uuid.MustParse("0199b300-0000-7000-8000-000000000042"),
		uuid.MustParse("0199b300-0000-7000-8000-000000000043"),
	}
	fixture := newMembershipConcurrencyFixture(t, "membership_worker_restart", recordIDs)
	fixture.cutover(t)
	fixture.service.backfillBatch = 1

	first, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 1 || first.Inserted != 1 || first.Ready || first.Done {
		t.Fatalf("first backfill batch = %#v", first)
	}

	rowConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rowConn.Release()
	rowTx, err := rowConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(rowTx)
	if _, err := rowTx.Exec(fixture.ctx, `select id from app.membership_worker_restart
		where id = $1 for update`, recordIDs[1]); err != nil {
		t.Fatal(err)
	}

	workerCtx, cancelWorker := context.WithCancel(fixture.ctx)
	workerResult := make(chan membershipConcurrencyResult[BackfillProgress], 1)
	go func() {
		progress, err := fixture.service.Backfill(workerCtx)
		workerResult <- membershipConcurrencyResult[BackfillProgress]{value: progress, err: err}
	}()
	waitForMembershipPostgresLock(t, fixture.pool, 0, "membership_worker_restart")
	cancelWorker()
	interrupted := awaitRawMembershipConcurrencyResult(t, workerResult)
	if interrupted.err == nil {
		t.Fatalf("interrupted backfill returned %#v without an error", interrupted.value)
	}
	if err := rowTx.Rollback(fixture.ctx); err != nil && err != pgx.ErrTxClosed {
		t.Fatal(err)
	}

	var checkpoint uuid.UUID
	var lastAssigned, sidecars int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select
		backfill.checkpoint_record_id,
		backfill.last_assigned_ordinal,
		(select count(*) from pgbase_meta.record_creation_ordinals as ordinal
		 where ordinal.collection_id = backfill.collection_id)
		from pgbase_meta.record_membership_backfills as backfill
		where backfill.collection_id = $1`, fixture.collectionID).Scan(
		&checkpoint,
		&lastAssigned,
		&sidecars,
	); err != nil {
		t.Fatal(err)
	}
	if checkpoint != recordIDs[0] || lastAssigned != 1 || sidecars != 1 {
		t.Fatalf(
			"interrupted checkpoint/ordinal/sidecars = %s/%d/%d, want %s/1/1",
			checkpoint,
			lastAssigned,
			sidecars,
			recordIDs[0],
		)
	}

	restarted := New(fixture.pool)
	restarted.backfillBatch = 1
	for attempt := 0; ; attempt++ {
		progress, err := restarted.Backfill(fixture.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Done {
			break
		}
		if attempt == len(recordIDs)+1 {
			t.Fatalf("restarted backfill did not finish: %#v", progress)
		}
	}

	rows, err := fixture.pool.Query(fixture.ctx, `select record_id, ordinal
		from pgbase_meta.record_creation_ordinals
		where collection_id = $1 order by ordinal`, fixture.collectionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var recordID uuid.UUID
		var ordinal int64
		if err := rows.Scan(&recordID, &ordinal); err != nil {
			t.Fatal(err)
		}
		if index >= len(recordIDs) || recordID != recordIDs[index] || ordinal != int64(index+1) {
			t.Fatalf("restarted membership row %d = %s/%d", index, recordID, ordinal)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(recordIDs) {
		t.Fatalf("restarted membership row count = %d, want %d", index, len(recordIDs))
	}
}

func TestEnableV5CursorTTLStartsAfterCapabilityLockAcquisition(t *testing.T) {
	fixture := newMembershipConcurrencyFixture(t, "membership_enable_v5", nil)
	fixture.cutover(t)
	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Ready || !progress.Done {
		t.Fatalf("empty backfill progress = %#v", progress)
	}
	if _, err := fixture.service.PromoteReady(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.runtime_writer_leases
		where instance_id = $1`, fixture.runtime.FleetMember().InstanceID); err != nil {
		t.Fatal(err)
	}
	runtimeB, err := RegisterWriter(fixture.ctx, fixture.service, WriterB)
	if err != nil {
		t.Fatal(err)
	}

	capabilityConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilityConn.Release()
	capabilityTx, err := capabilityConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(capabilityTx)
	if _, err := capabilityTx.Exec(fixture.ctx, `select singleton
		from pgbase_meta.record_membership_capability where singleton for update`); err != nil {
		t.Fatal(err)
	}

	enableCtx, cancelEnable := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelEnable()
	enableResult := make(chan membershipConcurrencyResult[ActivationReceipt], 1)
	go func() {
		receipt, err := fixture.service.EnableV5(enableCtx, FleetProof{
			Members: []FleetMember{runtimeB.FleetMember()},
		})
		enableResult <- membershipConcurrencyResult[ActivationReceipt]{value: receipt, err: err}
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		0,
		"from pgbase_meta.record_membership_capability where singleton for update",
	)

	var releasedAfter time.Time
	if err := capabilityTx.QueryRow(fixture.ctx, `select clock_timestamp()`).Scan(&releasedAfter); err != nil {
		t.Fatal(err)
	}
	if err := capabilityTx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	receipt := awaitMembershipConcurrencyResult(t, enableResult)
	if receipt.EnabledAt.Before(releasedAfter) {
		t.Fatalf(
			"v5 enabled at %s before capability lock release marker %s",
			receipt.EnabledAt,
			releasedAfter,
		)
	}
	if receipt.DecodeV4Until.Sub(receipt.EnabledAt) != CursorTTL {
		t.Fatalf(
			"v4 decode interval = %s, want %s",
			receipt.DecodeV4Until.Sub(receipt.EnabledAt),
			CursorTTL,
		)
	}
}

type membershipConcurrencyFixture struct {
	ctx          context.Context
	pool         *database.Pool
	service      *Service
	runtime      *Runtime
	writer       Writer
	collectionID uuid.UUID
	tableName    string
	createdAt    time.Time
}

func newMembershipConcurrencyFixture(
	t *testing.T,
	tableName string,
	recordIDs []uuid.UUID,
) membershipConcurrencyFixture {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}

	collectionID := uuid.MustParse("0199b300-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 6, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $2, 'active', $3, $3)`, collectionID, tableName, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
		collectionID,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	table := pgx.Identifier{"app", tableName}.Sanitize()
	if _, err := pool.Exec(ctx, `create table `+table+` (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}
	for index, recordID := range recordIDs {
		recordTime := createdAt.Add(time.Duration(index) * time.Microsecond)
		if _, err := pool.Exec(ctx, `insert into `+table+`
			(id, created_at, updated_at) values ($1, $2, $2)`, recordID, recordTime); err != nil {
			t.Fatal(err)
		}
	}
	service := New(pool)
	runtime, err := RegisterWriter(ctx, service, WriterA)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := runtime.Writer()
	if err != nil {
		t.Fatal(err)
	}
	return membershipConcurrencyFixture{
		ctx:          ctx,
		pool:         pool,
		service:      service,
		runtime:      runtime,
		writer:       writer,
		collectionID: collectionID,
		tableName:    tableName,
		createdAt:    createdAt,
	}
}

func (f membershipConcurrencyFixture) cutover(t *testing.T) CutoverReceipt {
	t.Helper()
	receipt, err := f.service.Cutover(f.ctx, FleetProof{
		Members: []FleetMember{f.runtime.FleetMember()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func (f membershipConcurrencyFixture) assertNoBaseOrSidecar(t *testing.T, recordID uuid.UUID) {
	t.Helper()
	table := pgx.Identifier{"app", f.tableName}.Sanitize()
	var baseRows, sidecarRows int64
	if err := f.pool.QueryRow(f.ctx, `select
		(select count(*) from `+table+` where id = $2),
		(select count(*) from pgbase_meta.record_creation_ordinals
		 where collection_id = $1 and record_id = $2)`, f.collectionID, recordID).Scan(
		&baseRows,
		&sidecarRows,
	); err != nil {
		t.Fatal(err)
	}
	if baseRows != 0 || sidecarRows != 0 {
		t.Fatalf("base/sidecar rows = %d/%d, want 0/0", baseRows, sidecarRows)
	}
}

type membershipConcurrencyResult[T any] struct {
	value T
	err   error
}

func awaitMembershipConcurrencyResult[T any](
	t *testing.T,
	result <-chan membershipConcurrencyResult[T],
) T {
	t.Helper()
	completed := awaitRawMembershipConcurrencyResult(t, result)
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	return completed.value
}

func awaitRawMembershipConcurrencyResult[T any](
	t *testing.T,
	result <-chan membershipConcurrencyResult[T],
) membershipConcurrencyResult[T] {
	t.Helper()
	select {
	case completed := <-result:
		return completed
	case <-time.After(12 * time.Second):
		t.Fatal("timed out waiting for PostgreSQL concurrency schedule")
		var zero membershipConcurrencyResult[T]
		return zero
	}
}

func awaitMembershipConcurrencyError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(12 * time.Second):
		t.Fatal("timed out waiting for PostgreSQL concurrency schedule")
		return nil
	}
}

func waitForMembershipPostgresLock(
	t *testing.T,
	pool *database.Pool,
	backendPID int32,
	queryFragment string,
) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waitingPID int32
		err := pool.QueryRow(ctx, `select coalesce((
			select pid
			from pg_stat_activity
			where datname = current_database()
			  and ($1::integer = 0 or pid = $1)
			  and wait_event_type = 'Lock'
			  and position($2 in query) > 0
			order by pid
			limit 1
		), 0)`, backendPID, queryFragment).Scan(&waitingPID)
		if err == nil && waitingPID != 0 {
			return waitingPID
		}
		if err != nil && ctx.Err() == nil {
			t.Fatalf("inspect PostgreSQL lock wait: %v", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf(
				"PostgreSQL backend %d did not wait on a lock while running %q",
				backendPID,
				queryFragment,
			)
		}
	}
}

func membershipConcurrencyBackendPID(t *testing.T, tx pgx.Tx) int32 {
	t.Helper()
	var pid int32
	if err := tx.QueryRow(context.Background(), `select pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func rollbackMembershipConcurrencyTx(tx pgx.Tx) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
