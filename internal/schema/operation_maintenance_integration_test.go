//go:build integration

package schema

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

func TestSchemaOperationRunReconcilesMissingAuditAsFailed(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000701")
	insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
		operationID: operationID,
		actorID:     administratorID,
		status:      OperationRunning,
		baseVersion: 0,
		createdAt:   time.Now().UTC().Add(-3 * time.Minute),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForMaintenanceStatus(t, pool, operationID, OperationFailed, OperationFailureReconcileFailed)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestSchemaOperationRunTreatsACompetingTargetVersionAuditAsNoOwnAudit(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000703")
	insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
		operationID: operationID,
		actorID:     administratorID,
		status:      OperationRunning,
		baseVersion: 0,
		createdAt:   time.Now().UTC().Add(-3 * time.Minute),
	})
	insertMaintenanceAudit(t, pool,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000704"), administratorID, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForMaintenanceStatus(t, pool, operationID, OperationFailed, OperationFailureReconcileFailed)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() with competing audit: %v", err)
	}
}

func TestSchemaOperationRunLeavesItsOwnMismatchedAuditNonterminal(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000711")
	insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
		operationID: operationID,
		actorID:     administratorID,
		status:      OperationRunning,
		baseVersion: 0,
		createdAt:   time.Now().UTC().Add(-3 * time.Minute),
	})
	insertMaintenanceAudit(t, pool, operationID, administratorID, 1)
	if _, err := pool.Exec(context.Background(), `UPDATE pgbase_meta.schema_migrations
		SET operation = 'collection_update'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Run(ctx); !errors.Is(err, errSchemaOperationAuditIntegrity) {
		t.Fatalf("Run() error = %v, want audit integrity violation", err)
	}

	var status OperationStatus
	var failureCode *OperationFailureCode
	var completedAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT status, failure_code, completed_at
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, operationID).Scan(&status, &failureCode, &completedAt); err != nil {
		t.Fatal(err)
	}
	if status != OperationRunning || failureCode != nil || completedAt != nil {
		t.Fatalf("mismatched audit changed operation to status=%s failure=%v completed=%v",
			status, failureCode, completedAt)
	}
}

func TestSchemaOperationRunReconcilesExactAuditAsCommitted(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000702")
	insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
		operationID: operationID,
		actorID:     administratorID,
		status:      OperationRunning,
		baseVersion: 0,
		createdAt:   time.Now().UTC().Add(-3 * time.Minute),
	})
	insertMaintenanceAudit(t, pool, operationID, administratorID, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitForMaintenanceStatus(t, pool, operationID, OperationCommitted, "")
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}

	var exactLink bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS (
		SELECT 1
		FROM pgbase_meta.schema_operations AS operation
		JOIN pgbase_meta.schema_migrations AS audit
		  ON audit.version = operation.committed_version
		 AND audit.operation_id = operation.operation_id
		 AND audit.operation = operation.operation
		 AND audit.checksum = operation.plan_checksum
		 AND audit.applied_by = operation.actor_id
		WHERE operation.operation_id = $1
	)`, operationID).Scan(&exactLink); err != nil {
		t.Fatal(err)
	}
	if !exactLink {
		t.Fatal("committed reconciliation did not retain its exact audit linkage")
	}
}

func TestSchemaOperationCleanupIsBoundedSkipsLocksAndRetainsRecentReceipts(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	now := time.Now().UTC()
	lockedOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000705")
	insertFailedMaintenanceReceipt(t, pool, lockedOperationID, administratorID, now.Add(-32*24*time.Hour))
	for index := 0; index < schemaOperationCleanupBatch-1; index++ {
		operationID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("expired-schema-operation-%d", index)))
		insertFailedMaintenanceReceipt(t, pool, operationID, administratorID, now.Add(-31*24*time.Hour))
	}
	committedOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000706")
	insertMaintenanceAudit(t, pool, committedOperationID, administratorID, 1)
	insertCommittedMaintenanceReceipt(t, pool, committedOperationID, administratorID, now.Add(-31*24*time.Hour))
	recentOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000707")
	insertFailedMaintenanceReceipt(t, pool, recentOperationID, administratorID, now.Add(-29*24*time.Hour))

	ctx := context.Background()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lockingTx, err := connection.Begin(ctx)
	if err != nil {
		connection.Release()
		t.Fatal(err)
	}
	if _, err := lockingTx.Exec(ctx, `SELECT 1
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1
		FOR UPDATE`, lockedOperationID); err != nil {
		_ = lockingTx.Rollback(ctx)
		connection.Release()
		t.Fatal(err)
	}

	deleted, err := service.cleanupSchemaOperations(ctx)
	if err != nil {
		_ = lockingTx.Rollback(ctx)
		connection.Release()
		t.Fatal(err)
	}
	if deleted != schemaOperationCleanupBatch {
		t.Fatalf("first cleanup deleted %d receipts, want %d", deleted, schemaOperationCleanupBatch)
	}
	assertMaintenanceOperationExists(t, pool, lockedOperationID, true)
	assertMaintenanceOperationExists(t, pool, committedOperationID, false)
	assertMaintenanceOperationExists(t, pool, recentOperationID, true)

	if err := lockingTx.Rollback(ctx); err != nil {
		connection.Release()
		t.Fatal(err)
	}
	connection.Release()
	deleted, err = service.cleanupSchemaOperations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("second cleanup deleted %d receipts, want the unlocked remainder", deleted)
	}
	assertMaintenanceOperationExists(t, pool, lockedOperationID, false)
	assertMaintenanceOperationExists(t, pool, recentOperationID, true)
}

func TestSchemaOperationRunTreatsCanceledContextAsNormalShutdown(t *testing.T) {
	_, service, _ := newSchemaServiceTest(t, Planner{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run(canceled): %v", err)
	}
}

func TestSchemaOperationReconcileDefersAdvisoryContentionWithoutBlockingApply(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	change := NewCreateChange(CreateCollectionInput{Name: "maintenance_lock_order"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000708")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "schema-operation-key-0708")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.claimOperation(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET created_at = current_timestamp - interval '3 minutes',
			updated_at = current_timestamp - interval '3 minutes',
			lease_expires_at = current_timestamp - interval '1 minute',
			reconcile_after = current_timestamp - interval '50 seconds'
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatal(err)
	}

	blockerConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blockerConnection.Release()
	blocker, err := blockerConnection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blocker.Rollback(context.Background())
		}
	}()
	if _, err := blocker.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}

	deferContext, cancelDefer := context.WithTimeout(ctx, time.Second)
	processed, err := service.reconcileOneSchemaOperation(deferContext)
	cancelDefer()
	if err != nil || processed {
		t.Fatalf("reconcile under advisory contention = processed:%t err:%v, want deferred", processed, err)
	}

	type applyResult struct {
		result ApplyResult
		err    error
	}
	applied := make(chan applyResult, 1)
	go func() {
		relationOperation := false
		result, err := service.applyClaimedOperation(ctx, intent, claim, &relationOperation)
		applied <- applyResult{result: result, err: err}
	}()
	waitForAdvisoryLockWaiter(t, pool)

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	blockerOpen = false
	select {
	case got := <-applied:
		if got.err != nil || got.result.Receipt.Status != OperationCommitted {
			t.Fatalf("apply after reconciliation = %#v, %v", got.result, got.err)
		}
	case <-ctx.Done():
		t.Fatal("schema apply deadlocked behind reconciliation")
	}

	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.OperationStatus(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != OperationCommitted || receipt.FailureCode != "" {
		t.Fatalf("lock-order receipt = %#v", receipt)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, migrations: 1})
}

func TestSchemaOperationReconcileSkipsRowClaimedByAnotherInstance(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	createdAt := time.Now().UTC().Add(-3 * time.Minute)
	lockedOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000709")
	availableOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000710")
	for _, operationID := range []uuid.UUID{lockedOperationID, availableOperationID} {
		insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
			operationID: operationID,
			actorID:     administratorID,
			status:      OperationRunning,
			baseVersion: 0,
			createdAt:   createdAt,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	lockingTx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lockingTxOpen := true
	defer func() {
		if lockingTxOpen {
			_ = lockingTx.Rollback(context.Background())
		}
	}()
	if _, err := lockingTx.Exec(ctx, `SELECT 1
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1
		FOR UPDATE`, lockedOperationID); err != nil {
		t.Fatal(err)
	}

	processed, err := service.reconcileOneSchemaOperation(ctx)
	if err != nil || !processed {
		t.Fatalf("reconcile with first row claimed = processed:%t err:%v", processed, err)
	}
	waitForMaintenanceStatus(t, pool, availableOperationID, OperationFailed, OperationFailureReconcileFailed)
	var lockedStatus OperationStatus
	if err := pool.QueryRow(ctx, `SELECT status
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, lockedOperationID).Scan(&lockedStatus); err != nil {
		t.Fatal(err)
	}
	if lockedStatus != OperationRunning {
		t.Fatalf("claimed operation status = %s, want running", lockedStatus)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	lockingTxOpen = false
	processed, err = service.reconcileOneSchemaOperation(ctx)
	if err != nil || !processed {
		t.Fatalf("reconcile after claim release = processed:%t err:%v", processed, err)
	}
	waitForMaintenanceStatus(t, pool, lockedOperationID, OperationFailed, OperationFailureReconcileFailed)
}

func TestSchemaOperationReconcileDrainsAcrossServiceInstances(t *testing.T) {
	pool, first, administratorID := newSchemaServiceTest(t, Planner{})
	second := NewService(pool, &Cache{}, Planner{})
	createdAt := time.Now().UTC().Add(-3 * time.Minute)
	operationIDs := []uuid.UUID{
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000711"),
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000712"),
	}
	for _, operationID := range operationIDs {
		insertMaintenanceOperation(t, pool, maintenanceOperationFixture{
			operationID: operationID,
			actorID:     administratorID,
			status:      OperationRunning,
			baseVersion: 0,
			createdAt:   createdAt,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, instance := range []*Service{first, second} {
		instance := instance
		go func() {
			<-start
			results <- instance.reconcileSchemaOperations(ctx)
		}()
	}
	close(start)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("multi-instance reconciliation did not make progress")
		}
	}
	for _, operationID := range operationIDs {
		waitForMaintenanceStatus(t, pool, operationID, OperationFailed, OperationFailureReconcileFailed)
	}
}

type maintenanceOperationFixture struct {
	operationID uuid.UUID
	actorID     uuid.UUID
	status      OperationStatus
	baseVersion int64
	createdAt   time.Time
}

func insertMaintenanceOperation(t *testing.T, pool *database.Pool, fixture maintenanceOperationFixture) {
	t.Helper()
	leaseOwner := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fixture.operationID.String()+"-lease"))
	resourceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fixture.operationID.String()+"-resource"))
	resourceHash := sha256.Sum256(append([]byte("resource:"), fixture.operationID[:]...))
	idempotencyHash := sha256.Sum256(append([]byte("idempotency:"), fixture.operationID[:]...))
	createdAt := fixture.createdAt.UTC()
	leaseExpiresAt := createdAt.Add(2 * time.Minute)
	reconcileAfter := leaseExpiresAt.Add(10 * time.Second)
	if _, err := pool.Exec(context.Background(), `INSERT INTO pgbase_meta.schema_operations (
		operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
		resource_key_hash, operation, idempotency_key_hash, request_hash,
		plan_checksum, base_version, status, fence_generation, lease_owner,
		lease_expires_at, reconcile_after, committed_version, failure_code,
		created_at, updated_at, completed_at)
		VALUES ($1, 'guarded', 'administrator', $2, 'collection', $3,
			$4, 'collection_create', $5, decode(repeat('33', 32), 'hex'),
			decode(repeat('44', 32), 'hex'), $6, $7, 1, $8, $9, $10,
			NULL, NULL, $11, $11, NULL)`,
		fixture.operationID, fixture.actorID, resourceID, resourceHash[:], idempotencyHash[:],
		fixture.baseVersion, fixture.status, leaseOwner, leaseExpiresAt, reconcileAfter, createdAt,
	); err != nil {
		t.Fatalf("insert maintenance operation: %v", err)
	}
}

func insertMaintenanceAudit(t *testing.T, pool *database.Pool, operationID, actorID uuid.UUID, version int64) {
	t.Helper()
	ctx := context.Background()
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.schema_state
			SET version = $1, updated_at = transaction_timestamp()
			WHERE singleton`, version); err != nil {
			return fmt.Errorf("advance schema state: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.schema_migrations (
			version, operation_id, operation, checksum, plan_json, sql_statements,
			applied_by, applied_at, execution_ms)
			VALUES ($1, $2, 'collection_create', decode(repeat('44', 32), 'hex'),
				'{}'::jsonb, ARRAY[]::text[], $3, transaction_timestamp(), 0)`,
			version, operationID, actorID,
		); err != nil {
			return fmt.Errorf("insert schema audit: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("insert maintenance audit: %v", err)
	}
}

func insertFailedMaintenanceReceipt(t *testing.T, pool *database.Pool, operationID, actorID uuid.UUID, completedAt time.Time) {
	t.Helper()
	completedAt = completedAt.UTC()
	createdAt := completedAt.Add(-time.Second)
	resourceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(operationID.String()+"-resource"))
	if _, err := pool.Exec(context.Background(), `INSERT INTO pgbase_meta.schema_operations (
		operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
		resource_key_hash, operation, idempotency_key_hash, request_hash,
		plan_checksum, base_version, status, fence_generation, lease_owner,
		lease_expires_at, reconcile_after, committed_version, failure_code,
		created_at, updated_at, completed_at)
		VALUES ($1, 'legacy', 'administrator', $2, 'collection', $3,
			decode(repeat('11', 32), 'hex'), 'collection_create', NULL,
			decode(repeat('33', 32), 'hex'), decode(repeat('44', 32), 'hex'),
			0, 'failed', 1, NULL, NULL, NULL, NULL, 'reconcile_failed',
			$4, $5, $5)`, operationID, actorID, resourceID, createdAt, completedAt); err != nil {
		t.Fatalf("insert failed maintenance receipt: %v", err)
	}
}

func insertCommittedMaintenanceReceipt(t *testing.T, pool *database.Pool, operationID, actorID uuid.UUID, completedAt time.Time) {
	t.Helper()
	completedAt = completedAt.UTC()
	createdAt := completedAt.Add(-time.Second)
	resourceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(operationID.String()+"-resource"))
	if _, err := pool.Exec(context.Background(), `INSERT INTO pgbase_meta.schema_operations (
		operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
		resource_key_hash, operation, idempotency_key_hash, request_hash,
		plan_checksum, base_version, status, fence_generation, lease_owner,
		lease_expires_at, reconcile_after, committed_version, failure_code,
		created_at, updated_at, completed_at)
		VALUES ($1, 'legacy', 'administrator', $2, 'collection', $3,
			decode(repeat('11', 32), 'hex'), 'collection_create', NULL,
			decode(repeat('33', 32), 'hex'), decode(repeat('44', 32), 'hex'),
			0, 'committed', 1, NULL, NULL, NULL, 1, NULL,
			$4, $5, $5)`, operationID, actorID, resourceID, createdAt, completedAt); err != nil {
		t.Fatalf("insert committed maintenance receipt: %v", err)
	}
}

func assertMaintenanceOperationExists(t *testing.T, pool *database.Pool, operationID uuid.UUID, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS (
		SELECT 1 FROM pgbase_meta.schema_operations WHERE operation_id = $1
	)`, operationID).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != want {
		t.Fatalf("operation %s exists = %t, want %t", operationID, exists, want)
	}
}

func waitForMaintenanceStatus(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, operationID uuid.UUID, wantStatus OperationStatus, wantFailure OperationFailureCode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status OperationStatus
		var failureCode *OperationFailureCode
		var terminalShape bool
		err := pool.QueryRow(context.Background(), `SELECT status, failure_code,
			lease_owner IS NULL AND lease_expires_at IS NULL
				AND reconcile_after IS NULL AND completed_at = updated_at
		FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, operationID).Scan(&status, &failureCode, &terminalShape)
		if err == nil && status == wantStatus && terminalShape {
			if wantFailure == "" && failureCode == nil {
				return
			}
			if failureCode != nil && *failureCode == wantFailure {
				return
			}
		}
		if err != nil {
			t.Fatalf("read schema operation status: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not reach %s/%s", operationID, wantStatus, wantFailure)
}
