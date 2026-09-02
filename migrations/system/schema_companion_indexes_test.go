//go:build integration

package system_test

import (
	"context"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func newSchemaCompanionIndexSchema(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyMigrationsBefore(t, ctx, pool, 9)
	return ctx, pool
}

func TestSchemaCompanionIndexMigrationCreatesPendingCapabilityAndWriterLease(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)

	wantColumns := map[string]string{
		"schema_companion_index_capability": "singleton:boolean:NO,state:text:NO,generation:bigint:NO,catalog_version:bigint:YES,building_at:timestamp with time zone:YES,ready_at:timestamp with time zone:YES,ready_index_count:bigint:YES,updated_at:timestamp with time zone:NO",
		"schema_writer_leases":              "instance_id:uuid:NO,compiler_version:smallint:NO,started_at:timestamp with time zone:NO,heartbeat_at:timestamp with time zone:NO,lease_expires_at:timestamp with time zone:NO",
	}
	for table, want := range wantColumns {
		var got string
		if err := pool.QueryRow(ctx, `SELECT string_agg(
			column_name || ':' || data_type || ':' || is_nullable,
			',' ORDER BY ordinal_position)
			FROM information_schema.columns
			WHERE table_schema = 'pgbase_meta' AND table_name = $1`, table).Scan(&got); err != nil {
			t.Fatalf("read %s columns: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s columns=%q want=%q", table, got, want)
		}
	}

	// The rollout must begin with the gate closed, so a binary that reaches a
	// freshly migrated database never assumes the companion contract.
	var state string
	var generation int64
	var clear bool
	if err := pool.QueryRow(ctx, `SELECT state, generation,
		catalog_version IS NULL AND building_at IS NULL
			AND ready_at IS NULL AND ready_index_count IS NULL
		FROM pgbase_meta.schema_companion_index_capability
		WHERE singleton`).Scan(&state, &generation, &clear); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || generation != 0 || !clear {
		t.Fatalf("initial capability state=%q generation=%d clear=%t", state, generation, clear)
	}

	var leases int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pgbase_meta.schema_writer_leases`).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != 0 {
		t.Fatalf("seeded schema writer leases=%d, want 0", leases)
	}

	var expiryIndex string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'pgbase_meta' AND indexname = 'schema_writer_leases_expiry_idx'`).
		Scan(&expiryIndex); err != nil {
		t.Fatalf("read schema writer lease expiry index: %v", err)
	}
}

// The migration runs inside the runner's transaction, so it must not create a
// dynamic collection index and must not reach for CREATE INDEX CONCURRENTLY.
// Holding ACCESS EXCLUSIVE on a collection table proves both: either would
// block on this lock and exhaust the deadline instead of committing.
func TestSchemaCompanionIndexMigrationDoesNotTouchDynamicCollectionTables(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyMigrationsBefore(t, ctx, pool, 8)

	const collectionID = "019551d0-e9c0-7000-8000-000000000811"
	if _, err := pool.Exec(ctx, `CREATE TABLE app.sortable_records (
		id uuid PRIMARY KEY,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL,
		nickname text
	);
	CREATE INDEX idx_sortable_nickname ON app.sortable_records (nickname);
	INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
	VALUES ('`+collectionID+`', 'sortable_records', 'sortable_records', 'active',
		transaction_timestamp(), transaction_timestamp());
	INSERT INTO app.sortable_records (id, created_at, updated_at, nickname)
	VALUES ('019551d0-e9c0-7000-8000-000000000812',
		transaction_timestamp(), transaction_timestamp(), 'first')`); err != nil {
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
		"LOCK TABLE app.sortable_records IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		t.Fatal(err)
	}
	migrationContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := pool.Exec(migrationContext, migrationSQL(t, 8)); err != nil {
		t.Fatalf("v8 accessed the locked dynamic table: %v", err)
	}

	var companionIndexes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'app' AND tablename = 'sortable_records'
			AND indexname <> 'sortable_records_pkey'
			AND indexname <> 'idx_sortable_nickname'`).Scan(&companionIndexes); err != nil {
		t.Fatal(err)
	}
	if companionIndexes != 0 {
		t.Fatalf("migration created %d dynamic collection indexes, want 0", companionIndexes)
	}
}

func TestSchemaCompanionIndexMigrationEnforcesCapabilityRolloutStates(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)

	// pending may follow a completed rollout, so it is constrained only by the
	// fields that must be clear, never by generation or by being first.
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'pending', generation = 4, updated_at = transaction_timestamp()
		WHERE singleton`); err != nil {
		t.Fatalf("pending after a previous rollout must stay legal: %v", err)
	}

	for name, statement := range map[string]string{
		"unknown state": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'activating', updated_at = transaction_timestamp() WHERE singleton`,
		"pending keeps a build timestamp": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'pending', building_at = transaction_timestamp(),
			    updated_at = transaction_timestamp() WHERE singleton`,
		"pending keeps a catalog version": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'pending', catalog_version = 3,
			    updated_at = transaction_timestamp() WHERE singleton`,
		"building without a catalog version": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'building', generation = 1, building_at = transaction_timestamp(),
			    updated_at = transaction_timestamp() WHERE singleton`,
		"building at generation zero": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'building', generation = 0, catalog_version = 3,
			    building_at = transaction_timestamp(),
			    updated_at = transaction_timestamp() WHERE singleton`,
		"building already ready": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'building', generation = 1, catalog_version = 3,
			    building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
			    updated_at = transaction_timestamp() WHERE singleton`,
		"ready without a validated count": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'ready', generation = 1, catalog_version = 3,
			    building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
			    updated_at = transaction_timestamp() WHERE singleton`,
		"ready without ever building": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'ready', generation = 1, catalog_version = 3,
			    ready_at = transaction_timestamp(), ready_index_count = 2,
			    updated_at = transaction_timestamp() WHERE singleton`,
		"negative generation": `UPDATE pgbase_meta.schema_companion_index_capability
			SET generation = -1, updated_at = transaction_timestamp() WHERE singleton`,
		"negative ready index count": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'ready', generation = 1, catalog_version = 3,
			    building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
			    ready_index_count = -1, updated_at = transaction_timestamp() WHERE singleton`,
	} {
		t.Run(name, func(t *testing.T) {
			requireSQLState(t, ctx, pool, statement, "23514")
		})
	}

	var second int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT 1 FROM pgbase_meta.schema_companion_index_capability) AS rows`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second != 1 {
		t.Fatalf("capability rows=%d, want the singleton only", second)
	}
	requireSQLState(t, ctx, pool,
		`INSERT INTO pgbase_meta.schema_companion_index_capability
			(singleton, state, generation, updated_at)
		 VALUES (false, 'pending', 0, transaction_timestamp())`, "23514")
}

func TestSchemaCompanionIndexMigrationRejectsImpossibleCapabilityTimelines(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)

	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'ready', generation = 1, catalog_version = 3,
		    building_at = transaction_timestamp() - interval '2 minutes',
		    ready_at = transaction_timestamp() - interval '1 minute',
		    ready_index_count = 2, updated_at = transaction_timestamp()
		WHERE singleton`); err != nil {
		t.Fatalf("a well ordered ready timeline must be accepted: %v", err)
	}

	for name, statement := range map[string]string{
		"ready before building": `UPDATE pgbase_meta.schema_companion_index_capability
			SET ready_at = building_at - interval '1 second' WHERE singleton`,
		"update before building": `UPDATE pgbase_meta.schema_companion_index_capability
			SET updated_at = building_at - interval '1 second' WHERE singleton`,
		"update before ready": `UPDATE pgbase_meta.schema_companion_index_capability
			SET updated_at = ready_at - interval '1 second' WHERE singleton`,
	} {
		t.Run(name, func(t *testing.T) {
			requireSQLState(t, ctx, pool, statement, "23514")
		})
	}
}

func TestSchemaCompanionIndexMigrationBoundsWriterLeases(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)

	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000821', 1,
			transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + interval '90 seconds')`); err != nil {
		t.Fatalf("a bounded lease must be accepted: %v", err)
	}

	for name, statement := range map[string]string{
		"heartbeat before start": `INSERT INTO pgbase_meta.schema_writer_leases
			(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000822', 1,
				transaction_timestamp(), transaction_timestamp() - interval '1 second',
				transaction_timestamp())`,
		"expiry beyond the heartbeat window": `INSERT INTO pgbase_meta.schema_writer_leases
			(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000823', 1,
				transaction_timestamp(), transaction_timestamp(),
				transaction_timestamp() + interval '3 minutes')`,
		"expiry before start": `INSERT INTO pgbase_meta.schema_writer_leases
			(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000824', 1,
				transaction_timestamp(), transaction_timestamp(),
				transaction_timestamp() - interval '1 second')`,
		"compiler version below the first release": `INSERT INTO pgbase_meta.schema_writer_leases
			(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000825', 0,
				transaction_timestamp(), transaction_timestamp(),
				transaction_timestamp())`,
	} {
		t.Run(name, func(t *testing.T) {
			requireSQLState(t, ctx, pool, statement, "23514")
		})
	}

	// A newer compiler must not need a migration just to name itself, so the
	// version column is deliberately unbounded above.
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000826', 7,
			transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + interval '30 seconds')`); err != nil {
		t.Fatalf("a future compiler version must be storable: %v", err)
	}
}

// The capability this migration installs decides whether a schema writer may
// issue the new plan format, so it must not commit beside an in-flight
// collection create that the writer has already planned under the old one.
func TestSchemaCompanionIndexMigrationWaitsForConcurrentCollectionCreateFence(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyMigrationsBefore(t, ctx, pool, 8)

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	collectionCreate, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = collectionCreate.Rollback(ctx) }()
	if _, err := collectionCreate.Exec(ctx, `SELECT version
		FROM pgbase_meta.schema_state WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := pool.Exec(ctx, migrationSQL(t, 8))
		done <- execErr
	}()
	select {
	case err := <-done:
		t.Fatalf("v8 returned before the collection creation fence committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := collectionCreate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("v8 did not continue after the collection creation fence committed")
	}
}
