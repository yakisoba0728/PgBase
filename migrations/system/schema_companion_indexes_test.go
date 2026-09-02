//go:build integration

package system_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// Migration 9. A capability left 'ready' by the previous binary rests on an
// activation proof that proved nothing: that binary declared compiler_version 2 while
// issuing v1 plans only, and so did the binary that issues v2, so the fleet check
// could not tell them apart. There is no honest generation to backfill, because the
// proof never observed one -- so the row is demoted and the fleet re-proved.
func TestSchemaCompilerGenerationMigrationDemotesAnUnprovableRollout(t *testing.T) {
	for _, state := range []string{"ready", "building"} {
		t.Run(state, func(t *testing.T) {
			ctx, pool := newSchemaCompanionIndexSchema(t)
			seedCompanionRolloutAtV8(t, ctx, pool, state)

			if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
				t.Fatalf("apply migration 9: %v", err)
			}

			var got, generation int64
			var demoted string
			var clear bool
			if err := pool.QueryRow(ctx, `SELECT state, generation,
				catalog_version IS NULL AND building_at IS NULL AND ready_at IS NULL
					AND ready_index_count IS NULL AND proven_compiler_version IS NULL,
				(SELECT count(*) FROM pgbase_meta.schema_companion_index_capability)
				FROM pgbase_meta.schema_companion_index_capability WHERE singleton`).
				Scan(&demoted, &generation, &clear, &got); err != nil {
				t.Fatal(err)
			}
			if demoted != "pending" || !clear || got != 1 {
				t.Fatalf("capability after migration 9 state=%q clear=%t rows=%d", demoted, clear, got)
			}
			// The generation counter is a rollout count, not a proof, so the next
			// activation is the next generation rather than a repeat of this one.
			if generation != 1 {
				t.Fatalf("rollout generation after migration 9 = %d, want 1", generation)
			}

			// The indexes the demoted rollout built are deliberately kept: they are
			// correct objects, and only the claim that the fleet converged is void.
			var indexes int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes
				WHERE schemaname = 'app' AND indexname = 'companion_probe_idx'`).Scan(&indexes); err != nil {
				t.Fatal(err)
			}
			if indexes != 1 {
				t.Fatalf("migration 9 dropped a built companion index (found %d)", indexes)
			}
		})
	}
}

// Migration 9 waits for a predecessor that already holds the physical
// maintenance lock. This is a bounded in-flight drain, not proof that every old
// process has passed its precheck or a durable post-commit exclusion mechanism.
func TestSchemaCompilerGenerationMigrationWaitsForPreP0PhysicalMaintenanceLock(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	seedCompanionRolloutAtV8(t, ctx, pool, "building")
	// Must match internal/schema.companionIndexAdvisoryKey. The SQL migration
	// embeds its decimal value because a migration cannot import a Go constant.
	const companionMaintenanceLock int64 = 0x7067626173650008
	assertMigrationWaitsForAdvisoryDrain(t, ctx, pool, companionMaintenanceLock,
		"live predecessor maintenance")
	var state string
	var indexValid bool
	if err := pool.QueryRow(ctx, `SELECT capability.state,
		indexes.indisvalid AND indexes.indisready AND indexes.indislive
		FROM pgbase_meta.schema_companion_index_capability AS capability
		JOIN pg_class AS index_table ON index_table.relname = 'companion_probe_idx'
		JOIN pg_index AS indexes ON indexes.indexrelid = index_table.oid
		WHERE capability.singleton`).Scan(&state, &indexValid); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || !indexValid {
		t.Fatalf("post-drain migration state=%q index_valid=%t", state, indexValid)
	}
}

// This characterizes the boundary that the deployment freeze/drain must close.
// A predecessor can cache v8's building state before taking the maintenance
// lock, pause through M, then take the released lock and change a physical index.
// The database cannot prevent that tokenless DDL, but its durable state remains
// fail-closed: v9 demotes to pending and rejects the old promotion statement.
func TestSchemaCompilerGenerationMigrationAllowsStalePrecheckPhysicalDDLButKeepsCapabilityFailClosed(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	seedCompanionRolloutAtV8(t, ctx, pool, "building")
	if _, err := pool.Exec(ctx, "DROP INDEX app.companion_probe_idx"); err != nil {
		t.Fatalf("make the cached physical plan require a build: %v", err)
	}

	var cachedState string
	if err := pool.QueryRow(ctx, `SELECT state
		FROM pgbase_meta.schema_companion_index_capability
		WHERE singleton`).Scan(&cachedState); err != nil {
		t.Fatalf("predecessor status precheck: %v", err)
	}
	if cachedState != "building" {
		t.Fatalf("predecessor cached state = %q, want building", cachedState)
	}
	if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
		t.Fatalf("apply migration 9 while predecessor is before its lock: %v", err)
	}

	maintenance, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Release()
	const companionMaintenanceLock int64 = 0x7067626173650008
	if _, err := maintenance.Exec(ctx, "SELECT pg_advisory_lock($1)", companionMaintenanceLock); err != nil {
		t.Fatalf("predecessor acquire maintenance lock after M: %v", err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			_, _ = maintenance.Exec(context.Background(),
				"SELECT pg_advisory_unlock($1)", companionMaintenanceLock)
		}
	}()
	if _, err := maintenance.Exec(ctx, `CREATE INDEX CONCURRENTLY companion_probe_idx
		ON app.companion_probe (nickname DESC NULLS LAST, id DESC)`); err != nil {
		t.Fatalf("stale predecessor physical build after M: %v", err)
	}
	if _, err := maintenance.Exec(ctx,
		"SELECT pg_advisory_unlock($1)", companionMaintenanceLock); err != nil {
		t.Fatalf("predecessor release maintenance lock: %v", err)
	}
	unlocked = true

	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'ready', ready_at = transaction_timestamp(),
			ready_index_count = 1, updated_at = transaction_timestamp()
		WHERE singleton`, "23514")
	var state string
	var indexValid bool
	if err := pool.QueryRow(ctx, `SELECT capability.state,
		indexes.indisvalid AND indexes.indisready AND indexes.indislive
		FROM pgbase_meta.schema_companion_index_capability AS capability
		JOIN pg_class AS index_table ON index_table.relname = 'companion_probe_idx'
		JOIN pg_index AS indexes ON indexes.indexrelid = index_table.oid
		WHERE capability.singleton`).Scan(&state, &indexValid); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || !indexValid {
		t.Fatalf("stale precheck boundary state=%q index_valid=%t", state, indexValid)
	}
}

// Apply and rollout transitions take the schema advisory lock before either
// the schema-state row or the writer-lease table. M must join that order before
// it locks both objects, otherwise an in-flight writer can form a row/table lock
// cycle with the migration.
func TestSchemaCompilerGenerationMigrationDrainsSchemaWritersBeforeCatalogLocks(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	// Must match internal/schema.schemaAdvisoryLockKey.
	const schemaLock int64 = 0x7067626173650002
	assertMigrationWaitsForAdvisoryDrain(t, ctx, pool, schemaLock, "live schema writer")
}

func assertMigrationWaitsForAdvisoryDrain(
	t *testing.T, ctx context.Context, pool *database.Pool, lockKey int64, blockedBoundary string,
) {
	t.Helper()
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	migrationConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer migrationConnection.Release()
	if _, err := blocker.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		t.Fatal(err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			_, _ = blocker.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
		}
	}()

	var migrationPID int
	if err := migrationConnection.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&migrationPID); err != nil {
		t.Fatal(err)
	}
	migration := migrationSQL(t, 9)
	done := make(chan error, 1)
	go func() {
		_, execErr := migrationConnection.Exec(ctx, migration)
		done <- execErr
	}()

	waitContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		select {
		case err := <-done:
			t.Fatalf("migration 9 crossed %s: %v", blockedBoundary, err)
		default:
		}
		var waiting bool
		err := pool.QueryRow(waitContext, `SELECT coalesce((SELECT
			wait_event_type = 'Lock' AND wait_event = 'advisory'
			FROM pg_stat_activity WHERE pid = $1), false)`, migrationPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("observe migration drain of %s: %v", blockedBoundary, err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := blocker.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
		t.Fatal(err)
	}
	unlocked = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("migration 9 after draining %s: %v", blockedBoundary, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("migration 9 did not continue after draining %s", blockedBoundary)
	}
}

// Migration 9 is additive for a database that never opened a rollout, which is the
// state a freshly migrated one is in.
func TestSchemaCompilerGenerationMigrationLeavesAPendingCapabilityAlone(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at
		FROM pgbase_meta.schema_companion_index_capability WHERE singleton`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
		t.Fatalf("apply migration 9: %v", err)
	}
	var after time.Time
	var state string
	if err := pool.QueryRow(ctx, `SELECT state, updated_at
		FROM pgbase_meta.schema_companion_index_capability WHERE singleton`).Scan(&state, &after); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || !after.Equal(before) {
		t.Fatalf("pending capability state=%q updated_at moved=%t", state, !after.Equal(before))
	}
}

// The constraint migration 9 adds is the thing that keeps the proved generation from
// disagreeing with state: a non-pending row must name one, and a pending row must not.
func TestSchemaCompilerGenerationConstraintTiesTheProofToTheState(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
		t.Fatalf("apply migration 9: %v", err)
	}
	for name, statement := range map[string]string{
		"building without a proved generation": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'building_v3', generation = 1, catalog_version = 0,
				building_at = transaction_timestamp(), updated_at = transaction_timestamp()
			WHERE singleton`,
		"pending naming a proved generation": `UPDATE pgbase_meta.schema_companion_index_capability
			SET proven_compiler_version = 3 WHERE singleton`,
		"proved generation below one": `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'building_v3', generation = 1, catalog_version = 0,
				proven_compiler_version = 0, building_at = transaction_timestamp(),
				updated_at = transaction_timestamp()
			WHERE singleton`,
	} {
		t.Run(name, func(t *testing.T) {
			requireSQLState(t, ctx, pool, statement, "23514")
		})
	}
}

// The predecessor's maintenance command recognizes the v8 "building" label
// and promotes it with this exact state update. Migration 9 must make that
// storage transition unrepresentable so an old checker cannot reopen ready.
func TestSchemaCompilerGenerationMigrationFencesThePredecessorPromotion(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
		t.Fatalf("apply migration 9: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'building_v3', generation = 1, catalog_version = 0,
			proven_compiler_version = 3, building_at = transaction_timestamp(),
			updated_at = transaction_timestamp() WHERE singleton`); err != nil {
		t.Fatalf("open current rollout: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'ready', ready_at = transaction_timestamp(),
			ready_index_count = 0, updated_at = transaction_timestamp()
		WHERE singleton`, "23514")
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM pgbase_meta.schema_companion_index_capability
		WHERE singleton`).Scan(&state); err != nil || state != "building_v3" {
		t.Fatalf("capability after predecessor promotion = %q, %v", state, err)
	}
}

// A pre-P0 process can remain alive across M even after its last generation-2
// lease expires. Its old upsert must fail at the database seam before that
// process can commit schema DDL under a generation-3 capability.
func TestSchemaCompilerGenerationMigrationFencesThePreP0WriterProtocol(t *testing.T) {
	ctx, pool := newSchemaCompanionIndexSchema(t)
	const oldUpsert = `INSERT INTO pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000829', 2,
			transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + interval '90 seconds')
		ON CONFLICT (instance_id) DO UPDATE SET
			heartbeat_at = excluded.heartbeat_at,
			lease_expires_at = excluded.lease_expires_at`
	if _, err := pool.Exec(ctx, oldUpsert); err != nil {
		t.Fatalf("seed pre-P0 lease: %v", err)
	}
	if _, err := pool.Exec(ctx, migrationSQL(t, 9)); err != nil {
		t.Fatalf("apply migration 9: %v", err)
	}
	var leases, protocol int
	if err := pool.QueryRow(ctx, `SELECT count(*), min(writer_protocol)
		FROM pgbase_meta.schema_writer_leases`).Scan(&leases, &protocol); err != nil {
		t.Fatal(err)
	}
	if leases != 1 || protocol != 1 {
		t.Fatalf("migration 9 preserved leases = %d at protocol %d", leases, protocol)
	}
	requireSQLState(t, ctx, pool, oldUpsert, "23502")
	err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, oldUpsert); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE public.pre_p0_schema_ddl (id bigint PRIMARY KEY)`)
		return err
	})
	requireErrorSQLState(t, err, "23502")
	var catalogChanged bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.pre_p0_schema_ddl') IS NOT NULL`).Scan(&catalogChanged); err != nil {
		t.Fatal(err)
	}
	if catalogChanged {
		t.Fatal("pre-P0 schema DDL committed after migration 9")
	}
	for _, invalid := range []int{0, 2} {
		requireSQLState(t, ctx, pool, fmt.Sprintf(`UPDATE pgbase_meta.schema_writer_leases
			SET writer_protocol = %d`, invalid), "23514")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at, writer_protocol)
		VALUES ('019551d0-e9c0-7000-8000-000000000829', 2,
			transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + interval '90 seconds', 1)
		ON CONFLICT (instance_id) DO UPDATE SET writer_protocol = excluded.writer_protocol`); err != nil {
		t.Fatalf("register P0 writer protocol: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT writer_protocol FROM pgbase_meta.schema_writer_leases
		WHERE instance_id = '019551d0-e9c0-7000-8000-000000000829'`).Scan(&protocol); err != nil || protocol != 1 {
		t.Fatalf("P0 writer protocol = %d, %v", protocol, err)
	}
}

// seedCompanionRolloutAtV8 puts the capability in the state the previous binary could
// leave it in, using the columns migration 8 shipped and nothing later. The index
// stands in for a companion that rollout built.
func seedCompanionRolloutAtV8(t *testing.T, ctx context.Context, pool *database.Pool, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`CREATE TABLE app.companion_probe (id uuid PRIMARY KEY, nickname text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE INDEX companion_probe_idx
		ON app.companion_probe (nickname DESC NULLS LAST, id DESC)`); err != nil {
		t.Fatal(err)
	}
	statement := `UPDATE pgbase_meta.schema_companion_index_capability
		SET state = 'building', generation = 1, catalog_version = 0,
			building_at = transaction_timestamp(), updated_at = transaction_timestamp()
		WHERE singleton`
	if state == "ready" {
		statement = `UPDATE pgbase_meta.schema_companion_index_capability
			SET state = 'ready', generation = 1, catalog_version = 0,
				building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
				ready_index_count = 1, updated_at = transaction_timestamp()
			WHERE singleton`
	}
	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatalf("seed a %s rollout at v8: %v", state, err)
	}
}
