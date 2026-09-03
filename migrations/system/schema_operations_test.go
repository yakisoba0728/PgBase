//go:build integration

package system_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

const insertRunningSchemaOperationSQL = `INSERT INTO pgbase_meta.schema_operations (
	operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
	resource_key_hash, operation, idempotency_key_hash, request_hash,
	plan_checksum, base_version, status, fence_generation, lease_owner,
	lease_expires_at, reconcile_after, committed_version, failure_code,
	created_at, updated_at, completed_at)
	VALUES ($1, $2, 'administrator', $3, 'collection', $4,
		decode(repeat('11', 32), 'hex'), 'collection_create', $5,
		decode(repeat('22', 32), 'hex'), decode(repeat('33', 32), 'hex'),
		0, 'running', 1, $6,
		transaction_timestamp() + interval '2 minutes',
		transaction_timestamp() + interval '2 minutes 10 seconds',
		NULL, NULL, transaction_timestamp(), transaction_timestamp(), NULL)`

func TestSchemaOperationsMigrationCreatesBoundedLedgerColumns(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)

	var columns string
	if err := pool.QueryRow(ctx, `SELECT string_agg(
		column_name || ':' || data_type || ':' || is_nullable,
		',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = 'pgbase_meta'
		  AND table_name = 'schema_operations'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	want := "operation_id:uuid:NO," +
		"mode:text:NO," +
		"actor_kind:text:NO," +
		"actor_id:uuid:NO," +
		"resource_kind:text:NO," +
		"resource_id:uuid:NO," +
		"resource_key_hash:bytea:NO," +
		"operation:text:NO," +
		"idempotency_key_hash:bytea:YES," +
		"request_hash:bytea:NO," +
		"plan_checksum:bytea:NO," +
		"base_version:bigint:NO," +
		"status:text:NO," +
		"fence_generation:bigint:NO," +
		"lease_owner:uuid:YES," +
		"lease_expires_at:timestamp with time zone:YES," +
		"reconcile_after:timestamp with time zone:YES," +
		"committed_version:bigint:YES," +
		"failure_code:text:YES," +
		"created_at:timestamp with time zone:NO," +
		"updated_at:timestamp with time zone:NO," +
		"completed_at:timestamp with time zone:YES"
	if columns != want {
		t.Fatalf("schema_operations columns = %q, want %q", columns, want)
	}
}

func TestSchemaOperationsMigrationLinksCommittedReceiptsToExactAuditRows(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)

	var uniqueColumns string
	if err := pool.QueryRow(ctx, `SELECT string_agg(attribute.attname, ',' ORDER BY key.ordinality)
		FROM pg_constraint AS constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey) WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute AS attribute
		  ON attribute.attrelid = constraint_record.conrelid
		 AND attribute.attnum = key.attnum
		WHERE constraint_record.conrelid = 'pgbase_meta.schema_migrations'::regclass
		  AND constraint_record.conname = 'schema_migrations_receipt_unique'
		  AND constraint_record.contype = 'u'`).Scan(&uniqueColumns); err != nil {
		t.Fatal(err)
	}
	if uniqueColumns != "version,operation_id,operation,checksum,applied_by" {
		t.Fatalf("receipt unique columns = %q", uniqueColumns)
	}

	var sourceColumns, targetColumns string
	var deferrable, initiallyDeferred bool
	if err := pool.QueryRow(ctx, `SELECT
		string_agg(source_attribute.attname, ',' ORDER BY key.ordinality),
		string_agg(target_attribute.attname, ',' ORDER BY key.ordinality),
		constraint_record.condeferrable,
		constraint_record.condeferred
		FROM pg_constraint AS constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey) WITH ORDINALITY AS key(attnum, ordinality)
		JOIN pg_attribute AS source_attribute
		  ON source_attribute.attrelid = constraint_record.conrelid
		 AND source_attribute.attnum = key.attnum
		JOIN pg_attribute AS target_attribute
		  ON target_attribute.attrelid = constraint_record.confrelid
		 AND target_attribute.attnum = constraint_record.confkey[key.ordinality]
		WHERE constraint_record.conrelid = 'pgbase_meta.schema_operations'::regclass
		  AND constraint_record.conname = 'schema_operations_migration_fk'
		  AND constraint_record.contype = 'f'
		GROUP BY constraint_record.condeferrable, constraint_record.condeferred`).Scan(
		&sourceColumns, &targetColumns, &deferrable, &initiallyDeferred,
	); err != nil {
		t.Fatal(err)
	}
	if sourceColumns != "committed_version,operation_id,operation,plan_checksum,actor_id" ||
		targetColumns != "version,operation_id,operation,checksum,applied_by" ||
		!deferrable || !initiallyDeferred {
		t.Fatalf("migration FK source=%q target=%q deferrable=%t initially_deferred=%t",
			sourceColumns, targetColumns, deferrable, initiallyDeferred)
	}

	var adminDeleteAction string
	if err := pool.QueryRow(ctx, `SELECT constraint_record.confdeltype::text
		FROM pg_constraint AS constraint_record
		WHERE constraint_record.conrelid = 'pgbase_meta.schema_operations'::regclass
		  AND constraint_record.conname = 'schema_operations_actor_fk'
		  AND constraint_record.contype = 'f'
		  AND constraint_record.confrelid = 'pgbase_meta.admins'::regclass`).Scan(&adminDeleteAction); err != nil {
		t.Fatal(err)
	}
	if adminDeleteAction != "r" {
		t.Fatalf("administrator delete action = %q, want RESTRICT", adminDeleteAction)
	}

	var resourceForeignKeys int
	if err := pool.QueryRow(ctx, `SELECT count(*)
		FROM pg_constraint AS constraint_record
		CROSS JOIN LATERAL unnest(constraint_record.conkey) AS key(attnum)
		JOIN pg_attribute AS attribute
		  ON attribute.attrelid = constraint_record.conrelid
		 AND attribute.attnum = key.attnum
		WHERE constraint_record.conrelid = 'pgbase_meta.schema_operations'::regclass
		  AND constraint_record.contype = 'f'
		  AND attribute.attname = 'resource_id'`).Scan(&resourceForeignKeys); err != nil {
		t.Fatal(err)
	}
	if resourceForeignKeys != 0 {
		t.Fatalf("resource_id foreign keys = %d, want 0", resourceForeignKeys)
	}
}

func TestSchemaOperationsMigrationAcceptsOnlyBoundedClaims(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	const (
		adminID     = "019551d0-e9c0-7000-8000-000000000401"
		operationID = "019551d0-e9c0-7000-8000-000000000402"
		resourceID  = "019551d0-e9c0-7000-8000-000000000403"
		leaseOwner  = "019551d0-e9c0-7000-8000-000000000404"
	)
	insertSchemaOperationAdmin(t, ctx, pool, adminID)
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		operationID, "guarded", adminID, resourceID,
		strings.Repeat("\x44", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert valid guarded claim: %v", err)
	}

	invalidUpdates := []struct {
		name      string
		statement string
	}{
		{"mode", `mode = 'automatic'`},
		{"actor kind", `actor_kind = 'service'`},
		{"resource kind", `resource_kind = 'record'`},
		{"resource key hash", `resource_key_hash = decode('11', 'hex')`},
		{"operation vocabulary", `operation = 'create'`},
		{"guarded key required", `idempotency_key_hash = NULL`},
		{"idempotency key hash", `idempotency_key_hash = decode('44', 'hex')`},
		{"request hash", `request_hash = decode('22', 'hex')`},
		{"plan checksum", `plan_checksum = decode('33', 'hex')`},
		{"base version", `base_version = -1`},
		{"status", `status = 'succeeded'`},
		{"fence generation", `fence_generation = 0`},
		{"running lease owner", `lease_owner = NULL`},
		{"running lease expiry", `lease_expires_at = updated_at`},
		{"running lease budget", `lease_expires_at = updated_at + interval '2 minutes 1 second', reconcile_after = updated_at + interval '2 minutes 10 seconds'`},
		{"running reconcile order", `reconcile_after = lease_expires_at - interval '1 second'`},
		{"running reconcile budget", `reconcile_after = lease_expires_at + interval '11 seconds'`},
		{"running committed version", `committed_version = 1`},
		{"running failure code", `failure_code = 'apply_failed'`},
		{"failure code vocabulary", `status = 'failed', lease_owner = NULL, lease_expires_at = NULL, reconcile_after = NULL, failure_code = 'raw_database_error', updated_at = created_at + interval '1 second', completed_at = created_at + interval '1 second'`},
		{"unknown reconcile budget", `status = 'unknown', lease_owner = NULL, lease_expires_at = NULL, reconcile_after = updated_at + interval '11 seconds'`},
		{"running completion", `completed_at = updated_at`},
		{"timestamp order", `updated_at = created_at - interval '1 second'`},
	}
	for _, testCase := range invalidUpdates {
		t.Run(testCase.name, func(t *testing.T) {
			requireSQLState(t, ctx, pool,
				`UPDATE pgbase_meta.schema_operations SET `+testCase.statement+` WHERE operation_id = '`+operationID+`'`,
				"23514",
			)
		})
	}

	const legacyOperationID = "019551d0-e9c0-7000-8000-000000000405"
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		legacyOperationID, "legacy", adminID, resourceID, nil, leaseOwner,
	); err != nil {
		t.Fatalf("insert valid legacy claim: %v", err)
	}
	requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.schema_operations
		SET idempotency_key_hash = decode(repeat('44', 32), 'hex')
		WHERE operation_id = '`+legacyOperationID+`'`, "23514")
}

func TestSchemaOperationsMigrationAcceptsRunningToUnknownTransition(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	const (
		adminID     = "019551d0-e9c0-7000-8000-000000000406"
		operationID = "019551d0-e9c0-7000-8000-000000000407"
		resourceID  = "019551d0-e9c0-7000-8000-000000000408"
		leaseOwner  = "019551d0-e9c0-7000-8000-000000000409"
	)
	insertSchemaOperationAdmin(t, ctx, pool, adminID)
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		operationID, "guarded", adminID, resourceID,
		strings.Repeat("\x44", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert running operation: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET status = 'unknown',
			lease_owner = NULL,
			lease_expires_at = NULL,
			reconcile_after = created_at + interval '11 seconds',
			updated_at = created_at + interval '1 second'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("transition running operation to unknown: %v", err)
	}

	var status string
	var leaseCleared, reconcileBounded, receiptCleared, completionCleared bool
	if err := pool.QueryRow(ctx, `SELECT
		status,
		lease_owner IS NULL AND lease_expires_at IS NULL,
		reconcile_after >= updated_at
			AND reconcile_after <= updated_at + interval '10 seconds',
		committed_version IS NULL AND failure_code IS NULL,
		completed_at IS NULL
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, operationID).Scan(
		&status, &leaseCleared, &reconcileBounded, &receiptCleared, &completionCleared,
	); err != nil {
		t.Fatal(err)
	}
	if status != "unknown" || !leaseCleared || !reconcileBounded || !receiptCleared || !completionCleared {
		t.Fatalf("unknown transition status=%q lease_cleared=%t reconcile_bounded=%t receipt_cleared=%t completion_cleared=%t",
			status, leaseCleared, reconcileBounded, receiptCleared, completionCleared)
	}
}

func TestSchemaOperationsMigrationAcceptsRunningToFailedTransition(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	const (
		adminID     = "019551d0-e9c0-7000-8000-00000000040a"
		operationID = "019551d0-e9c0-7000-8000-00000000040b"
		resourceID  = "019551d0-e9c0-7000-8000-00000000040c"
		leaseOwner  = "019551d0-e9c0-7000-8000-00000000040d"
	)
	insertSchemaOperationAdmin(t, ctx, pool, adminID)
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		operationID, "guarded", adminID, resourceID,
		strings.Repeat("\x44", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert running operation: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET status = 'failed',
			lease_owner = NULL,
			lease_expires_at = NULL,
			reconcile_after = NULL,
			failure_code = 'apply_failed',
			updated_at = created_at + interval '1 second',
			completed_at = created_at + interval '1 second'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("transition running operation to failed: %v", err)
	}

	var status, failureCode string
	var leaseCleared, reconcileCleared, receiptCleared, completionBounded bool
	if err := pool.QueryRow(ctx, `SELECT
		status,
		failure_code,
		lease_owner IS NULL AND lease_expires_at IS NULL,
		reconcile_after IS NULL,
		committed_version IS NULL,
		completed_at = updated_at
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, operationID).Scan(
		&status, &failureCode, &leaseCleared, &reconcileCleared, &receiptCleared, &completionBounded,
	); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || failureCode != "apply_failed" || !leaseCleared || !reconcileCleared || !receiptCleared || !completionBounded {
		t.Fatalf("failed transition status=%q failure_code=%q lease_cleared=%t reconcile_cleared=%t receipt_cleared=%t completion_bounded=%t",
			status, failureCode, leaseCleared, reconcileCleared, receiptCleared, completionBounded)
	}
}

func TestSchemaOperationsMigrationCreatesBoundedWorkerAndHistoryIndexes(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	tests := []struct {
		name             string
		columns          string
		unique           bool
		definitionParts  []string
		predicatePresent bool
	}{
		{
			name:             "schema_operations_guarded_identity_unique",
			columns:          "actor_kind,actor_id,resource_kind,resource_key_hash,idempotency_key_hash",
			unique:           true,
			definitionParts:  []string{"WHERE", "mode", "guarded"},
			predicatePresent: true,
		},
		{
			name:            "schema_operations_owner_history_idx",
			columns:         "actor_kind,actor_id,created_at,operation_id",
			definitionParts: []string{"created_at DESC", "operation_id DESC"},
		},
		{
			name:             "schema_operations_reconcile_idx",
			columns:          "reconcile_after,operation_id",
			definitionParts:  []string{"WHERE", "running", "unknown"},
			predicatePresent: true,
		},
		{
			name:             "schema_operations_cleanup_idx",
			columns:          "completed_at,operation_id",
			definitionParts:  []string{"WHERE", "committed", "failed"},
			predicatePresent: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var columns, definition string
			var unique, predicatePresent bool
			if err := pool.QueryRow(ctx, `SELECT
				string_agg(attribute.attname, ',' ORDER BY key.ordinality),
				index_record.indisunique,
				index_record.indpred IS NOT NULL,
				pg_get_indexdef(index_record.indexrelid)
				FROM pg_index AS index_record
				CROSS JOIN LATERAL unnest(index_record.indkey) WITH ORDINALITY AS key(attnum, ordinality)
				JOIN pg_attribute AS attribute
				  ON attribute.attrelid = index_record.indrelid
				 AND attribute.attnum = key.attnum
				WHERE index_record.indexrelid = ('pgbase_meta.' || $1)::regclass
				GROUP BY index_record.indisunique, index_record.indpred, index_record.indexrelid`,
				testCase.name,
			).Scan(&columns, &unique, &predicatePresent, &definition); err != nil {
				t.Fatal(err)
			}
			if columns != testCase.columns || unique != testCase.unique || predicatePresent != testCase.predicatePresent {
				t.Fatalf("index columns=%q unique=%t predicate=%t",
					columns, unique, predicatePresent)
			}
			for _, part := range testCase.definitionParts {
				if !strings.Contains(definition, part) {
					t.Fatalf("index definition %q does not contain %q", definition, part)
				}
			}
		})
	}
}

func TestSchemaOperationsMigrationScopesGuardedIdentityButAllowsLegacyClaims(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	const (
		adminID    = "019551d0-e9c0-7000-8000-000000000411"
		resourceID = "019551d0-e9c0-7000-8000-000000000412"
		leaseOwner = "019551d0-e9c0-7000-8000-000000000413"
	)
	insertSchemaOperationAdmin(t, ctx, pool, adminID)
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		"019551d0-e9c0-7000-8000-000000000414", "guarded", adminID,
		resourceID, strings.Repeat("\x44", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert first guarded claim: %v", err)
	}
	_, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		"019551d0-e9c0-7000-8000-000000000415", "guarded", adminID,
		resourceID, strings.Repeat("\x44", 32), leaseOwner,
	)
	requirePostgresErrorCode(t, err, "23505")

	for _, operationID := range []string{
		"019551d0-e9c0-7000-8000-000000000416",
		"019551d0-e9c0-7000-8000-000000000417",
	} {
		if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
			operationID, "legacy", adminID, resourceID, nil, leaseOwner,
		); err != nil {
			t.Fatalf("insert legacy claim %s: %v", operationID, err)
		}
	}
}

func TestSchemaOperationsMigrationDefersAndEnforcesExactAuditReceipt(t *testing.T) {
	ctx, pool := newSchemaOperationsSchema(t)
	const (
		adminID     = "019551d0-e9c0-7000-8000-000000000421"
		operationID = "019551d0-e9c0-7000-8000-000000000422"
		resourceID  = "019551d0-e9c0-7000-8000-000000000423"
		leaseOwner  = "019551d0-e9c0-7000-8000-000000000424"
	)
	insertSchemaOperationAdmin(t, ctx, pool, adminID)
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		operationID, "guarded", adminID, resourceID,
		strings.Repeat("\x44", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert running receipt: %v", err)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET status = 'committed',
			lease_owner = NULL,
			lease_expires_at = NULL,
			reconcile_after = NULL,
			committed_version = 1,
			updated_at = created_at + interval '1 second',
			completed_at = created_at + interval '1 second'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("record receipt before audit: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.schema_migrations
		(version, operation_id, operation, checksum, plan_json, sql_statements,
		 applied_by, applied_at, execution_ms)
		VALUES (1, $1, 'collection_create', decode(repeat('33', 32), 'hex'),
			'{}'::jsonb, ARRAY[]::text[], $2, transaction_timestamp(), 0)`,
		operationID, adminID,
	); err != nil {
		t.Fatalf("insert matching schema audit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit deferred matching receipt: %v", err)
	}

	var ledgerOperation, auditOperation string
	if err := pool.QueryRow(ctx, `SELECT ledger.operation, audit.operation
		FROM pgbase_meta.schema_operations AS ledger
		JOIN pgbase_meta.schema_migrations AS audit
		  ON audit.version = ledger.committed_version
		 AND audit.operation_id = ledger.operation_id
		WHERE ledger.operation_id = $1`, operationID).Scan(
		&ledgerOperation, &auditOperation,
	); err != nil {
		t.Fatal(err)
	}
	if ledgerOperation != "collection_create" || ledgerOperation != auditOperation {
		t.Fatalf("ledger operation=%q audit operation=%q", ledgerOperation, auditOperation)
	}

	const mismatchedOperationID = "019551d0-e9c0-7000-8000-000000000425"
	if _, err := pool.Exec(ctx, insertRunningSchemaOperationSQL,
		mismatchedOperationID, "guarded", adminID, resourceID,
		strings.Repeat("\x45", 32), leaseOwner,
	); err != nil {
		t.Fatalf("insert mismatched running receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET base_version = 1 WHERE operation_id = $1`, mismatchedOperationID); err != nil {
		t.Fatal(err)
	}
	mismatchTx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mismatchTx.Rollback(ctx) }()
	if _, err := mismatchTx.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET operation = 'collection_update',
			status = 'committed',
			lease_owner = NULL,
			lease_expires_at = NULL,
			reconcile_after = NULL,
			committed_version = 2,
			updated_at = created_at + interval '1 second',
			completed_at = created_at + interval '1 second'
		WHERE operation_id = $1`, mismatchedOperationID); err != nil {
		t.Fatalf("record mismatched receipt: %v", err)
	}
	if _, err := mismatchTx.Exec(ctx, `INSERT INTO pgbase_meta.schema_migrations
		(version, operation_id, operation, checksum, plan_json, sql_statements,
		 applied_by, applied_at, execution_ms)
		VALUES (2, $1, 'collection_create', decode(repeat('33', 32), 'hex'),
			'{}'::jsonb, ARRAY[]::text[], $2, transaction_timestamp(), 0)`,
		mismatchedOperationID, adminID,
	); err != nil {
		t.Fatalf("insert mismatched schema audit: %v", err)
	}
	requirePostgresErrorCode(t, mismatchTx.Commit(ctx), "23503")
}

func insertSchemaOperationAdmin(t *testing.T, ctx context.Context, pool *database.Pool, administratorID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		VALUES ($1::uuid, $1::text || '@example.test', 'hash', true,
			transaction_timestamp(), transaction_timestamp())`, administratorID); err != nil {
		t.Fatalf("insert schema operation administrator: %v", err)
	}
}

func newSchemaOperationsSchema(t *testing.T) (context.Context, *database.Pool) {
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
