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
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestWriterFenceReservesAndDeletesMembershipAtomically(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID := uuid.MustParse("0199b200-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'writer_fence', 'writer_fence', 'active', $2, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table app.writer_fence (
		id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}

	runtimeA, err := RegisterWriter(ctx, New(pool), WriterA)
	if err != nil {
		t.Fatal(err)
	}
	writerA, err := runtimeA.Writer()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.LockCreate(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if err := fence.Reserve(ctx); err != nil {
			return err
		}
		return fence.RecordCreated(ctx, uuid.MustParse("0199b200-0000-7000-8000-000000000010"))
	}); err != nil {
		t.Fatal(err)
	}
	assertWriterMembershipCounts(t, pool, collectionID, 0, 0)

	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update pgbase_meta.collection_creation_counters
			set generation = 1, updated_at = $2 where collection_id = $1`, collectionID, createdAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.record_membership_backfills set
			generation = 1, state = 'backfilling', cutover_count = 0,
			updated_at = $2 where collection_id = $1`, collectionID, createdAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
			state = 'dual_write', generation = 1, minimum_writer_version = 1,
			cutover_at = $1, cutover_collection_count = 1, cutover_record_count = 0,
			updated_at = $1 where singleton`, createdAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	missingBaseRecord := uuid.MustParse("0199b200-0000-7000-8000-000000000009")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.LockCreate(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if err := fence.Reserve(ctx); err != nil {
			return err
		}
		return fence.RecordCreated(ctx, missingBaseRecord)
	})
	if err == nil {
		t.Fatal("membership create accepted a missing base row")
	}
	assertWriterMembershipCounts(t, pool, collectionID, 0, 0)

	rollbackSentinel := errors.New("roll back reserved membership")
	rolledBackRecord := uuid.MustParse("0199b200-0000-7000-8000-000000000011")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.LockCreate(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if err := fence.Reserve(ctx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into app.writer_fence (id, created_at, updated_at)
			values ($1, $2, $2)`, rolledBackRecord, createdAt); err != nil {
			return err
		}
		if err := fence.RecordCreated(ctx, rolledBackRecord); err != nil {
			return err
		}
		return rollbackSentinel
	})
	if !errors.Is(err, rollbackSentinel) {
		t.Fatalf("rolled-back create error = %v", err)
	}
	assertWriterMembershipCounts(t, pool, collectionID, 0, 0)

	createdRecord := uuid.MustParse("0199b200-0000-7000-8000-000000000012")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.LockCreate(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if err := fence.Reserve(ctx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into app.writer_fence (id, created_at, updated_at)
			values ($1, $2, $2)`, createdRecord, createdAt); err != nil {
			return err
		}
		return fence.RecordCreated(ctx, createdRecord)
	}); err != nil {
		t.Fatal(err)
	}
	assertWriterMembershipCounts(t, pool, collectionID, 1, 1)

	if _, err := RegisterWriter(ctx, New(pool), WriterA0); !errors.Is(err, ErrWriterTooOld) {
		t.Fatalf("A0 writer after cutover error = %v", err)
	}

	if _, err := pool.Exec(ctx, `update pgbase_meta.record_membership_backfills set
		state = 'ready', validated_base_count = 1, validated_sidecar_count = 1,
		completed_at = $2, updated_at = $2 where collection_id = $1`,
		collectionID, createdAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.record_membership_capability set
		state = 'ready', ready_at = $1, ready_collection_count = 1,
		updated_at = $1 where singleton`,
		createdAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.Begin(ctx, tx)
		if err != nil {
			return err
		}
		return fence.Delete(ctx, []Record{{CollectionID: collectionID, RecordID: createdRecord}})
	})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("membership delete before base delete error = %v", err)
	}
	assertWriterMembershipCounts(t, pool, collectionID, 1, 1)
	missingRecord := uuid.MustParse("0199b200-0000-7000-8000-000000000013")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		fence, err := writerA.Begin(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from app.writer_fence where id = $1`, createdRecord); err != nil {
			return err
		}
		return fence.Delete(ctx, []Record{
			{CollectionID: collectionID, RecordID: createdRecord},
			{CollectionID: collectionID, RecordID: missingRecord},
		})
	})
	if !errors.Is(err, ErrMembershipDrift) {
		t.Fatalf("ready delete with missing sidecar error = %v", err)
	}
	assertWriterMembershipCounts(t, pool, collectionID, 1, 1)
}

// Once a migration past record membership is embedded, the pre-membership v6
// history leaves this binary unable to checksum-verify the objects the resolver
// depends on, so every writer version refuses it — A0 included. The prefix rule
// this relies on is pinned directly in
// TestMembershipHistoryAcceptsCompleteKnownFuturePrefix.
func TestNoWriterAcceptsPreMembershipHistoryOnceALaterMigrationIsEmbedded(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	migrations := system.Migrations()
	if len(migrations) < 8 || migrations[5].Version != 6 || migrations[6].Version != 7 {
		t.Fatalf("membership manifest = %#v", migrations)
	}
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:6])
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for _, version := range []WriterVersion{WriterA0, WriterA, WriterB, WriterC} {
		if _, err := RegisterWriter(ctx, New(pool), version); err == nil {
			t.Fatalf("writer version %d accepted a v6 database", version)
		}
	}
}

func TestA0WriterRejectsEveryPartialMembershipInstallation(t *testing.T) {
	tables := []string{
		"record_membership_capability",
		"collection_creation_counters",
		"record_creation_ordinals",
		"record_membership_backfills",
		"runtime_writer_leases",
	}
	for _, missing := range tables {
		t.Run(missing, func(t *testing.T) {
			ctx := context.Background()
			pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := migrate.New(pool, nil).Up(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `drop table pgbase_meta.`+pgx.Identifier{missing}.Sanitize()+` cascade`); err != nil {
				t.Fatal(err)
			}

			_, err = RegisterWriter(ctx, New(pool), WriterA0)
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("A0 writer with missing %s error = %v", missing, err)
			}
		})
	}
}

func TestRuntimeRejectsCorruptCompleteMembershipHistory(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "v7 checksum mismatch", sql: `update pgbase_meta.system_migrations
			set checksum = decode(repeat('00', 32), 'hex') where version = 7`},
		{name: "history gap", sql: `delete from pgbase_meta.system_migrations where version = 6`},
		{name: "future version", sql: `insert into pgbase_meta.system_migrations
			(version, name, checksum, execution_ms)
			values (99, 'future', decode(repeat('00', 32), 'hex'), 0)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := migrate.New(pool, nil).Up(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, test.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := RegisterWriter(ctx, New(pool), WriterA); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("RegisterWriter error = %v", err)
			}
		})
	}
}

func assertWriterMembershipCounts(t *testing.T, pool *database.Pool, collectionID uuid.UUID, ordinal, rows int64) {
	t.Helper()
	var gotOrdinal, gotRows int64
	if err := pool.QueryRow(context.Background(), `select
		(select last_ordinal from pgbase_meta.collection_creation_counters where collection_id = $1),
		(select count(*) from pgbase_meta.record_creation_ordinals where collection_id = $1)`,
		collectionID).Scan(&gotOrdinal, &gotRows); err != nil {
		t.Fatal(err)
	}
	if gotOrdinal != ordinal || gotRows != rows {
		t.Fatalf("writer membership ordinal/rows = %d/%d, want %d/%d", gotOrdinal, gotRows, ordinal, rows)
	}
}
