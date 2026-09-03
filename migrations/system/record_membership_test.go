//go:build integration

package system_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestRecordMembershipMigrationCreatesFeatureOffMetadata(t *testing.T) {
	ctx, pool := newRecordMembershipSchema(t)

	wantColumns := map[string]string{
		"record_membership_capability": "singleton:boolean:NO,state:text:NO,generation:bigint:NO,minimum_writer_version:smallint:NO,cursor_issue_version:smallint:NO,cutover_at:timestamp with time zone:YES,cutover_collection_count:bigint:YES,cutover_record_count:bigint:YES,ready_at:timestamp with time zone:YES,ready_collection_count:bigint:YES,v5_enabled_at:timestamp with time zone:YES,v4_decode_until:timestamp with time zone:YES,v4_retired_at:timestamp with time zone:YES,updated_at:timestamp with time zone:NO",
		"collection_creation_counters": "collection_id:uuid:NO,generation:bigint:NO,last_ordinal:bigint:NO,updated_at:timestamp with time zone:NO",
		"record_creation_ordinals":     "collection_id:uuid:NO,record_id:uuid:NO,ordinal:bigint:NO",
		"record_membership_backfills":  "collection_id:uuid:NO,generation:bigint:NO,state:text:NO,cutover_count:bigint:YES,cutover_last_created_at:timestamp with time zone:YES,cutover_last_record_id:uuid:YES,checkpoint_created_at:timestamp with time zone:YES,checkpoint_record_id:uuid:YES,last_assigned_ordinal:bigint:NO,validated_base_count:bigint:YES,validated_sidecar_count:bigint:YES,updated_at:timestamp with time zone:NO,completed_at:timestamp with time zone:YES",
		"runtime_writer_leases":        "instance_id:uuid:NO,writer_version:smallint:NO,started_at:timestamp with time zone:NO,heartbeat_at:timestamp with time zone:NO,lease_expires_at:timestamp with time zone:NO",
	}
	for table, want := range wantColumns {
		var got string
		if err := pool.QueryRow(ctx, `SELECT string_agg(
			column_name || ':' || data_type || ':' || is_nullable,
			',' ORDER BY ordinal_position)
			FROM information_schema.columns
			WHERE table_schema = 'pgbase_meta' AND table_name = $1`, table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s columns = %q, want %q", table, got, want)
		}
	}

	var state string
	var generation, minimumWriterVersion int64
	var cursorIssueVersion int
	var rolloutTimestampsClear bool
	if err := pool.QueryRow(ctx, `SELECT state, generation, minimum_writer_version,
		cursor_issue_version,
			cutover_at IS NULL
				AND cutover_collection_count IS NULL AND cutover_record_count IS NULL
				AND ready_at IS NULL AND ready_collection_count IS NULL AND v5_enabled_at IS NULL
			AND v4_decode_until IS NULL AND v4_retired_at IS NULL
		FROM pgbase_meta.record_membership_capability
		WHERE singleton`).Scan(
		&state, &generation, &minimumWriterVersion, &cursorIssueVersion,
		&rolloutTimestampsClear,
	); err != nil {
		t.Fatal(err)
	}
	if state != "off" || generation != 0 || minimumWriterVersion != 0 ||
		cursorIssueVersion != 4 || !rolloutTimestampsClear {
		t.Fatalf("initial capability state=%q generation=%d minimum_writer=%d cursor=%d timestamps_clear=%t",
			state, generation, minimumWriterVersion, cursorIssueVersion, rolloutTimestampsClear)
	}
}

func TestRecordMembershipMigrationSeedsCatalogWithoutAccessingDynamicRows(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Pinned to version 7 rather than "the last migration": this asserts what
	// the record-membership migration itself does, so a later append must not
	// silently retarget it.
	applyMigrationsBefore(t, ctx, pool, 7)
	const collectionID = "019551d0-e9c0-7000-8000-000000000711"
	if _, err := pool.Exec(ctx, `CREATE TABLE app.existing_records (
		id uuid PRIMARY KEY,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	);
	INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
	VALUES ('`+collectionID+`', 'existing_records', 'existing_records', 'active',
		transaction_timestamp(), transaction_timestamp());
	INSERT INTO app.existing_records (id, created_at, updated_at)
	VALUES ('019551d0-e9c0-7000-8000-000000000712',
		transaction_timestamp(), transaction_timestamp())`); err != nil {
		t.Fatal(err)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	dynamicLock, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dynamicLock.Rollback(ctx) }()
	if _, err := dynamicLock.Exec(ctx,
		"LOCK TABLE app.existing_records IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		t.Fatal(err)
	}
	migrationContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := pool.Exec(migrationContext, migrationSQL(t, 7)); err != nil {
		t.Fatalf("v7 accessed the locked dynamic table: %v", err)
	}

	var counters, backfills, ordinals, dynamicRows int
	var generation, lastOrdinal int64
	var backfillState string
	var backfillStateClear bool
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM pgbase_meta.collection_creation_counters),
		(SELECT count(*) FROM pgbase_meta.record_membership_backfills),
		(SELECT count(*) FROM pgbase_meta.record_creation_ordinals),
		(SELECT generation FROM pgbase_meta.collection_creation_counters
		 WHERE collection_id = $1),
		(SELECT last_ordinal FROM pgbase_meta.collection_creation_counters
		 WHERE collection_id = $1),
		(SELECT state FROM pgbase_meta.record_membership_backfills
		 WHERE collection_id = $1),
		(SELECT cutover_count IS NULL
			AND cutover_last_created_at IS NULL AND cutover_last_record_id IS NULL
			AND checkpoint_created_at IS NULL AND checkpoint_record_id IS NULL
			AND last_assigned_ordinal = 0
			AND validated_base_count IS NULL AND validated_sidecar_count IS NULL
			AND completed_at IS NULL
		 FROM pgbase_meta.record_membership_backfills
		 WHERE collection_id = $1)`, collectionID).Scan(
		&counters, &backfills, &ordinals, &generation, &lastOrdinal, &backfillState,
		&backfillStateClear,
	); err != nil {
		t.Fatal(err)
	}
	if counters != 1 || backfills != 1 || ordinals != 0 || generation != 0 ||
		lastOrdinal != 0 || backfillState != "off" || !backfillStateClear {
		t.Fatalf("seed counters=%d backfills=%d ordinals=%d generation=%d last=%d state=%q clear=%t",
			counters, backfills, ordinals, generation, lastOrdinal, backfillState, backfillStateClear)
	}
	if err := dynamicLock.QueryRow(ctx, "SELECT count(*) FROM app.existing_records").Scan(&dynamicRows); err != nil {
		t.Fatal(err)
	}
	if dynamicRows != 1 {
		t.Fatalf("dynamic record rows = %d, want 1", dynamicRows)
	}
}

func TestRecordMembershipMigrationRejectsImpossibleStateTimelines(t *testing.T) {
	ctx, pool := newRecordMembershipSchema(t)
	const (
		collectionID = "019551d0-e9c0-7000-8000-000000000731"
		upperID      = "019551d0-e9c0-7000-8000-000000000732"
		checkpointID = "019551d0-e9c0-7000-8000-000000000733"
	)
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
	VALUES ($1, 'bounded_backfill', 'bounded_backfill', 'active',
		transaction_timestamp(), transaction_timestamp())`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at)
	VALUES ($1, 0, 0, transaction_timestamp())`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at)
	VALUES ($1, 0, 'off', transaction_timestamp())`, collectionID); err != nil {
		t.Fatal(err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_backfills
		SET state = 'backfilling'
		WHERE collection_id = '`+collectionID+`'`, "23514")

	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability SET
		state = 'dual_write', generation = 1, minimum_writer_version = 1,
		cutover_at = '2026-09-02T02:00:00Z',
		updated_at = '2026-09-02T01:59:59Z'
		WHERE singleton`, "23514")

	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.collection_creation_counters SET
			generation = 1, last_ordinal = 1, updated_at = transaction_timestamp()
			WHERE collection_id = $1`, collectionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE pgbase_meta.record_membership_backfills SET
			generation = 1, state = 'backfilling', cutover_count = 1,
			cutover_last_created_at = '2026-09-02T02:00:00Z',
			cutover_last_record_id = $2, updated_at = transaction_timestamp()
			WHERE collection_id = $1`, collectionID, upperID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_backfills SET
		checkpoint_created_at = '2026-09-02T02:00:01Z',
		checkpoint_record_id = '`+checkpointID+`'
		WHERE collection_id = '`+collectionID+`'`, "23514")
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_backfills SET
		last_assigned_ordinal = 2
		WHERE collection_id = '`+collectionID+`'`, "23514")
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_backfills SET
		state = 'ready', validated_base_count = 1, validated_sidecar_count = 2,
		completed_at = updated_at
		WHERE collection_id = '`+collectionID+`'`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_backfills SET
		state = 'ready', validated_base_count = 1, validated_sidecar_count = 1,
		updated_at = '2026-09-02T02:05:00Z', completed_at = '2026-09-02T02:05:00Z'
		WHERE collection_id = $1`, collectionID); err != nil {
		t.Fatalf("complete valid bounded backfill: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) VALUES ($1, $2, 1)`, collectionID, upperID); err != nil {
		t.Fatalf("insert valid record ordinal: %v", err)
	}
	requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) VALUES
		('`+collectionID+`', '`+checkpointID+`', 1)`, "23505")
	requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) VALUES
		('`+collectionID+`', '`+checkpointID+`', 0)`, "23514")
}

func TestRecordMembershipMigrationEnforcesCapabilityRolloutStates(t *testing.T) {
	ctx, pool := newRecordMembershipSchema(t)
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability
		SET state = 'dual_write' WHERE singleton`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_capability SET
		state = 'cutover', generation = 1, minimum_writer_version = 1,
		cutover_at = '2026-09-02T02:00:00Z', updated_at = '2026-09-02T02:00:00Z'
		WHERE singleton`); err != nil {
		t.Fatalf("enable valid cutover barrier state: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_capability SET
		state = 'dual_write', cutover_collection_count = 0, cutover_record_count = 0
		WHERE singleton`); err != nil {
		t.Fatalf("enable valid dual write state: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability
		SET state = 'ready' WHERE singleton`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_capability SET
		state = 'ready', ready_at = '2026-09-02T02:01:00Z', ready_collection_count = 0,
		updated_at = '2026-09-02T02:01:00Z' WHERE singleton`); err != nil {
		t.Fatalf("promote valid ready state: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability SET
		minimum_writer_version = 2, cursor_issue_version = 5
		WHERE singleton`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_capability SET
		minimum_writer_version = 2, cursor_issue_version = 5,
		v5_enabled_at = '2026-09-02T02:02:00Z',
		v4_decode_until = '2026-09-02T02:17:00Z',
		updated_at = '2026-09-02T02:02:00Z' WHERE singleton`); err != nil {
		t.Fatalf("enable valid cursor v5 state: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability SET
		v4_retired_at = '2026-09-02T02:16:59Z',
		updated_at = '2026-09-02T02:16:59Z' WHERE singleton`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_membership_capability SET
		v4_retired_at = '2026-09-02T02:17:01Z',
		updated_at = '2026-09-02T02:17:01Z' WHERE singleton`); err != nil {
		t.Fatalf("retire valid cursor v4 state: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_membership_capability SET
		minimum_writer_version = 1, cursor_issue_version = 4,
		v5_enabled_at = NULL, v4_decode_until = NULL
		WHERE singleton`, "23514")
}

func TestRecordMembershipMigrationBoundsWriterLeases(t *testing.T) {
	ctx, pool := newRecordMembershipSchema(t)
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.runtime_writer_leases
		(instance_id, writer_version, started_at, heartbeat_at, lease_expires_at)
	VALUES ('019551d0-e9c0-7000-8000-000000000741', 0,
		'2026-09-02T02:00:00Z', '2026-09-02T02:01:00Z', '2026-09-02T02:02:00Z')`); err != nil {
		t.Fatalf("insert valid writer lease: %v", err)
	}
	requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.runtime_writer_leases
		(instance_id, writer_version, started_at, heartbeat_at, lease_expires_at)
	VALUES ('019551d0-e9c0-7000-8000-000000000742', 4,
		'2026-09-02T02:00:00Z', '2026-09-02T02:01:00Z', '2026-09-02T02:02:00Z')`, "23514")
	requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.runtime_writer_leases
		(instance_id, writer_version, started_at, heartbeat_at, lease_expires_at)
	VALUES ('019551d0-e9c0-7000-8000-000000000743', 1,
		'2026-09-02T02:01:00Z', '2026-09-02T02:00:59Z', '2026-09-02T02:02:00Z')`, "23514")
	requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.runtime_writer_leases
		(instance_id, writer_version, started_at, heartbeat_at, lease_expires_at)
	VALUES ('019551d0-e9c0-7000-8000-000000000744', 1,
		'2026-09-02T02:00:00Z', '2026-09-02T02:01:00Z', '2026-09-02T02:03:01Z')`, "23514")
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.runtime_writer_leases
		SET lease_expires_at = started_at WHERE instance_id =
		'019551d0-e9c0-7000-8000-000000000741'`); err != nil {
		t.Fatalf("expire writer lease for a drain: %v", err)
	}
}

func TestRecordMembershipMigrationCreatesBoundedKeysAndWorkerIndexes(t *testing.T) {
	ctx, pool := newRecordMembershipSchema(t)

	var sourceColumns, targetColumns, deleteAction string
	var deferrable, initiallyDeferred bool
	if err := pool.QueryRow(ctx, `SELECT
		string_agg(source_attribute.attname, ',' ORDER BY key.ordinality),
		string_agg(target_attribute.attname, ',' ORDER BY key.ordinality),
		constraint_record.confdeltype::text,
		constraint_record.condeferrable,
		constraint_record.condeferred
		FROM pg_constraint AS constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey)
			WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute AS source_attribute
		  ON source_attribute.attrelid = constraint_record.conrelid
		 AND source_attribute.attnum = key.attnum
		JOIN pg_attribute AS target_attribute
		  ON target_attribute.attrelid = constraint_record.confrelid
		 AND target_attribute.attnum = constraint_record.confkey[key.ordinality]
		WHERE constraint_record.conrelid =
			'pgbase_meta.record_membership_backfills'::regclass
		  AND constraint_record.conname = 'record_membership_backfills_counter_fk'
		GROUP BY constraint_record.confdeltype, constraint_record.condeferrable,
			constraint_record.condeferred`).Scan(
		&sourceColumns, &targetColumns, &deleteAction, &deferrable, &initiallyDeferred,
	); err != nil {
		t.Fatal(err)
	}
	if sourceColumns != "collection_id,generation" ||
		targetColumns != "collection_id,generation" || deleteAction != "c" ||
		!deferrable || !initiallyDeferred {
		t.Fatalf("backfill FK source=%q target=%q delete=%q deferrable=%t initially_deferred=%t",
			sourceColumns, targetColumns, deleteAction, deferrable, initiallyDeferred)
	}

	tests := []struct {
		name            string
		columns         string
		unique          bool
		predicateParts  []string
		definitionParts []string
	}{
		{
			name:    "record_creation_ordinals_collection_ordinal_unique",
			columns: "collection_id,ordinal",
			unique:  true,
		},
		{
			name:           "record_membership_backfills_work_idx",
			columns:        "generation,collection_id",
			predicateParts: []string{"state", "backfilling"},
		},
		{
			name:            "runtime_writer_leases_expiry_idx",
			columns:         "lease_expires_at,writer_version",
			definitionParts: []string{"lease_expires_at", "writer_version"},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var columns, definition string
			var predicate *string
			var unique bool
			if err := pool.QueryRow(ctx, `SELECT
				string_agg(attribute.attname, ',' ORDER BY key.ordinality),
				index_record.indisunique,
				pg_get_expr(index_record.indpred, index_record.indrelid),
				pg_get_indexdef(index_record.indexrelid)
				FROM pg_index AS index_record
				CROSS JOIN LATERAL unnest(index_record.indkey)
					WITH ORDINALITY AS key(attnum, ordinality)
				JOIN pg_attribute AS attribute
				  ON attribute.attrelid = index_record.indrelid
				 AND attribute.attnum = key.attnum
				WHERE index_record.indexrelid = ('pgbase_meta.' || $1)::regclass
				GROUP BY index_record.indisunique, index_record.indpred,
					index_record.indrelid, index_record.indexrelid`, testCase.name).Scan(
				&columns, &unique, &predicate, &definition,
			); err != nil {
				t.Fatal(err)
			}
			if columns != testCase.columns || unique != testCase.unique {
				t.Fatalf("index columns=%q unique=%t definition=%q", columns, unique, definition)
			}
			for _, part := range testCase.predicateParts {
				if predicate == nil || !strings.Contains(*predicate, part) {
					t.Fatalf("index predicate=%v does not contain %q", predicate, part)
				}
			}
			for _, part := range testCase.definitionParts {
				if !strings.Contains(definition, part) {
					t.Fatalf("index definition %q does not contain %q", definition, part)
				}
			}
		})
	}
}

func newRecordMembershipSchema(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range system.Migrations() {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
	return ctx, pool
}
