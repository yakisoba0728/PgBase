//go:build integration

package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestMaintenanceStateMachineBackfillsCutoverSnapshotAndDrainsV4(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	migrations := system.Migrations()
	if len(migrations) < 7 || migrations[6].Version != 7 {
		t.Fatalf("membership migration manifest = %#v", migrations)
	}
	// Stop at v6 so the catalog rows this test inserts below exist before the
	// record-membership migration seeds its counters from them.
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:6])
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	firstCollection := uuid.MustParse("0199b100-0000-7000-8000-000000000001")
	secondCollection := uuid.MustParse("0199b100-0000-7000-8000-000000000002")
	firstRecord := uuid.MustParse("0199b100-0000-7000-8000-000000000011")
	secondRecord := uuid.MustParse("0199b100-0000-7000-8000-000000000012")
	createdAt := time.Date(2020, 9, 2, 1, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at) values
		($1, 'membership_a', 'membership_a', 'active', $3, $3),
		($2, 'membership_b', 'membership_b', 'active', $3, $3)`,
		firstCollection, secondCollection, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table app.membership_a (
		id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null
	); create table app.membership_b (
		id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app.membership_a (id, created_at, updated_at) values
		($1, $3, $3), ($2, $3 + interval '1 microsecond', $3 + interval '1 microsecond')`,
		firstRecord, secondRecord, createdAt); err != nil {
		t.Fatal(err)
	}
	// Every remaining migration, not an exact target: UpThrough accepts only the
	// latest embedded version, and this test needs the whole history above v6.
	// internal/migrate owns the exact-target contract.
	runner := migrate.New(pool, nil)
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}

	service := New(pool)
	service.backfillBatch = 1

	oldRuntime, err := RegisterWriter(ctx, service, WriterA0)
	if err != nil {
		t.Fatal(err)
	}
	oldWriter := oldRuntime.FleetMember()
	if _, err := service.Cutover(ctx, FleetProof{Members: []FleetMember{oldWriter}}); !errors.Is(err, ErrWriterFleetTooOld) {
		t.Fatalf("cutover with A0 lease error = %v, want old-writer rejection", err)
	}
	if _, err := pool.Exec(ctx, `delete from pgbase_meta.runtime_writer_leases
		where instance_id = $1`, oldWriter.InstanceID); err != nil {
		t.Fatal(err)
	}
	releaseARuntime, err := RegisterWriter(ctx, service, WriterA)
	if err != nil {
		t.Fatal(err)
	}
	releaseAWriter := releaseARuntime.FleetMember()
	cutover, err := service.Cutover(ctx, FleetProof{Members: []FleetMember{releaseAWriter}})
	if err != nil {
		t.Fatal(err)
	}
	if cutover.Generation != 1 || cutover.Collections != 2 || cutover.Records != 2 ||
		cutover.CutoverAt.IsZero() {
		t.Fatalf("cutover receipt = %#v", cutover)
	}
	cutoverReplay, err := service.Cutover(ctx, FleetProof{})
	if err != nil || cutoverReplay != cutover {
		t.Fatalf("cutover readback/error = %#v/%v, want %#v", cutoverReplay, err, cutover)
	}
	now := cutover.CutoverAt.Add(time.Minute)

	newRecord := uuid.MustParse("0199b100-0000-7000-8000-000000000013")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var ordinal int64
		if err := tx.QueryRow(ctx, `update pgbase_meta.collection_creation_counters
			set last_ordinal = last_ordinal + 1, updated_at = $2
			where collection_id = $1 returning last_ordinal`, firstCollection, now).Scan(&ordinal); err != nil {
			return err
		}
		if ordinal != 3 {
			t.Fatalf("post-cutover ordinal = %d, want 3", ordinal)
		}
		if _, err := tx.Exec(ctx, `insert into app.membership_a (id, created_at, updated_at)
			values ($1, $2, $2)`, newRecord, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
			(collection_id, record_id, ordinal) values ($1, $2, $3)`, firstCollection, newRecord, ordinal)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	for attempts := 0; attempts < 10; attempts++ {
		progress, err := service.Backfill(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Done {
			break
		}
		if attempts == 9 {
			t.Fatal("backfill did not finish")
		}
	}
	rows, err := pool.Query(ctx, `select record_id, ordinal
		from pgbase_meta.record_creation_ordinals
		where collection_id = $1 order by ordinal`, firstCollection)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantIDs := []uuid.UUID{firstRecord, secondRecord, newRecord}
	rowCount := 0
	for rows.Next() {
		var recordID uuid.UUID
		var ordinal int64
		if err := rows.Scan(&recordID, &ordinal); err != nil {
			t.Fatal(err)
		}
		if rowCount >= len(wantIDs) || recordID != wantIDs[rowCount] || ordinal != int64(rowCount+1) {
			t.Fatalf("ordinal row %d = %s/%d", rowCount, recordID, ordinal)
		}
		rowCount++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if rowCount != len(wantIDs) {
		t.Fatalf("ordinal row count = %d, want %d", rowCount, len(wantIDs))
	}

	ready, err := service.PromoteReady(ctx)
	if err != nil || ready.Generation != 1 || ready.Collections != 2 {
		t.Fatalf("ready receipt/error = %#v/%v", ready, err)
	}
	readyReplay, err := service.PromoteReady(ctx)
	if err != nil || readyReplay != ready {
		t.Fatalf("ready readback/error = %#v/%v, want %#v", readyReplay, err, ready)
	}
	if _, err := service.EnableV5(ctx, FleetProof{
		Members: []FleetMember{releaseAWriter},
	}); !errors.Is(err, ErrWriterFleetTooOld) {
		t.Fatalf("enable v5 with release A lease error = %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from pgbase_meta.runtime_writer_leases
		where instance_id = $1`, releaseAWriter.InstanceID); err != nil {
		t.Fatal(err)
	}
	releaseBRuntime, err := RegisterWriter(ctx, service, WriterB)
	if err != nil {
		t.Fatal(err)
	}
	proofB := FleetProof{Members: []FleetMember{releaseBRuntime.FleetMember()}}
	activation, err := service.EnableV5(ctx, proofB)
	if err != nil || activation.DecodeV4Until != activation.EnabledAt.Add(CursorTTL) {
		t.Fatalf("v5 activation/error = %#v/%v", activation, err)
	}
	activationReplay, err := service.EnableV5(ctx, FleetProof{})
	if err != nil || activationReplay != activation {
		t.Fatalf("v5 activation readback/error = %#v/%v, want %#v", activationReplay, err, activation)
	}
	rollback, err := service.DisableV5(ctx)
	if err != nil || rollback.CursorIssueVersion != 4 || rollback.MinimumWriterVersion != WriterA {
		t.Fatalf("v5 rollback/error = %#v/%v", rollback, err)
	}
	rollbackReplay, err := service.DisableV5(ctx)
	if err != nil || rollbackReplay != rollback {
		t.Fatalf("v5 rollback readback/error = %#v/%v, want %#v", rollbackReplay, err, rollback)
	}
	activation, err = service.EnableV5(ctx, proofB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetireV4(ctx, proofB); !errors.Is(err, ErrCursorDrainPending) {
		t.Fatalf("early v4 retirement error = %v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.record_membership_capability set
		cutover_at = transaction_timestamp() - interval '20 minutes',
		ready_at = transaction_timestamp() - interval '19 minutes',
		v5_enabled_at = transaction_timestamp() - interval '16 minutes',
		v4_decode_until = transaction_timestamp() - interval '1 minute',
		updated_at = transaction_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}
	retirement, err := service.RetireV4(ctx, proofB)
	if err != nil || retirement.RetiredAt.IsZero() {
		t.Fatalf("v4 retirement/error = %#v/%v", retirement, err)
	}
	retirementReplay, err := service.RetireV4(ctx, FleetProof{})
	if err != nil || retirementReplay != retirement {
		t.Fatalf("v4 retirement readback/error = %#v/%v, want %#v", retirementReplay, err, retirement)
	}
	status, err := service.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateReady || status.Generation != 1 || status.CursorIssueVersion != 5 ||
		status.MinimumWriterVersion != WriterB || status.V4RetiredAt == nil ||
		*status.V4RetiredAt != retirement.RetiredAt {
		t.Fatalf("final membership status = %#v", status)
	}
}
