//go:build integration

package schema

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestGuardedApplyCommitsAndReplaysOneExactLedgerReceipt(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{
		Name:   "guarded_posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText}},
	})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	firstOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000601")
	intent, err := GuardedApply(change, plan, administratorID, firstOperationID, "guarded-schema-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Apply(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Collection.ID == uuid.Nil || first.Receipt.OperationID != firstOperationID ||
		first.Receipt.CollectionID != first.Collection.ID || first.Receipt.Status != OperationCommitted ||
		first.Receipt.Operation != SchemaOperationCollectionCreate || first.Receipt.CommittedVersion != 1 {
		t.Fatalf("first guarded result = %#v", first)
	}
	assertExactLedgerAuditLink(t, pool, first.Receipt)

	replayOperationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000602")
	replayIntent, err := GuardedApply(change, plan, administratorID, replayOperationID, "guarded-schema-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Apply(ctx, replayIntent)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || !reflect.DeepEqual(replayed.Receipt, first.Receipt) || replayed.Receipt.OperationID == replayOperationID {
		t.Fatalf("replayed guarded result = %#v, first = %#v", replayed, first)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
	var operations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.schema_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Fatalf("operation rows after replay = %d, want 1", operations)
	}
}

func TestGuardedApplyRejectsTheSameIdentityForADifferentRequest(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	firstChange := NewCreateChange(CreateCollectionInput{
		Name:   "guarded_conflict",
		Fields: []FieldInput{{Name: "title", Type: TypeText}},
	})
	differentChange := NewCreateChange(CreateCollectionInput{
		Name:   "guarded_conflict",
		Fields: []FieldInput{{Name: "body", Type: TypeText}},
	})
	firstPlan, err := service.Preview(ctx, firstChange)
	if err != nil {
		t.Fatal(err)
	}
	differentPlan, err := service.Preview(ctx, differentChange)
	if err != nil {
		t.Fatal(err)
	}
	firstIntent, err := GuardedApply(firstChange, firstPlan, administratorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000611"), "guarded-schema-key-0011")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, firstIntent); err != nil {
		t.Fatal(err)
	}
	differentIntent, err := GuardedApply(differentChange, differentPlan, administratorID,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000612"), "guarded-schema-key-0011")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, differentIntent); !errors.Is(err, ErrOperationConflict) || !errors.Is(err, ErrConflict) {
		t.Fatalf("different guarded request error = %v, want operation conflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestGuardedApplyRejectsAnOperationIDUsedByHistoricalAudit(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	historicalChange := NewCreateChange(CreateCollectionInput{Name: "historical_operation_id"})
	historicalPlan, err := service.Preview(ctx, historicalChange)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := service.Apply(ctx, LegacyApply(historicalChange, historicalPlan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.schema_operations
		WHERE operation_id = $1`, historical.Receipt.OperationID); err != nil {
		t.Fatal(err)
	}

	change := NewCreateChange(CreateCollectionInput{Name: "colliding_operation_id"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := GuardedApply(change, plan, administratorID, historical.Receipt.OperationID,
		"guarded-schema-key-audit-collision")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, intent); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("historical operation ID collision error = %v, want operation conflict", err)
	}
	var claimExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pgbase_meta.schema_operations WHERE operation_id = $1
	)`, historical.Receipt.OperationID).Scan(&claimExists); err != nil {
		t.Fatal(err)
	}
	if claimExists {
		t.Fatal("historical audit collision created an operation claim")
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, migrations: 1})
}

func TestGuardedApplyRollbackLeavesOneRedactedFailedReceipt(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_rollback"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000621")
	const rawSecret = "guarded-secret-key-0021"
	intent, err := GuardedApply(change, plan, administratorID, operationID, rawSecret)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("injected DDL failure with sensitive detail")
	service.beforeDDL = func(context.Context, pgx.Tx) error { return sentinel }
	result, err := service.Apply(ctx, intent)
	if !errors.Is(err, sentinel) {
		t.Fatalf("guarded rollback error = %v, want sentinel", err)
	}
	if result.Receipt.OperationID != operationID || result.Receipt.Status != OperationFailed ||
		result.Receipt.FailureCode != OperationFailureApplyFailed || result.Receipt.CompletedAt == nil {
		t.Fatalf("failed guarded receipt = %#v", result.Receipt)
	}
	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.OperationStatus(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, result.Receipt) {
		t.Fatalf("stored failed receipt = %#v, result = %#v", stored, result.Receipt)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pgbase_meta.schema_operations
		WHERE operation_id = $1
		  AND (operation LIKE '%' || $2 || '%'
		    OR encode(request_hash, 'escape') LIKE '%' || $2 || '%'
		    OR encode(idempotency_key_hash, 'escape') LIKE '%' || $2 || '%')
	)`, operationID, rawSecret).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("operation ledger retained a raw idempotency secret")
	}
}

func TestGuardedApplyReadsCommittedReceiptAfterCommitAcknowledgementLoss(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_ack_loss"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000631")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "guarded-schema-key-0031")
	if err != nil {
		t.Fatal(err)
	}
	ackLost := errors.New("commit acknowledgement lost")
	service.commitTx = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ackLost
	}
	result, err := service.Apply(ctx, intent)
	if err != nil {
		t.Fatalf("commit readback reported failure: %v", err)
	}
	if result.Receipt.Status != OperationCommitted || result.Receipt.OperationID != operationID {
		t.Fatalf("commit readback result = %#v", result)
	}
	assertExactLedgerAuditLink(t, pool, result.Receipt)
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, migrations: 1})
	if published := service.cache.Load(); published.Version != 1 || published.Collections["guarded_ack_loss"].Name == "" {
		t.Fatalf("commit readback did not publish catalog: %#v", published)
	}
}

func TestGuardedApplyMarksUnknownWhenAnAmbiguousCommitActuallyRollsBack(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_unknown"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000641")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "guarded-schema-key-0041")
	if err != nil {
		t.Fatal(err)
	}
	ackLost := errors.New("ambiguous rollback fixture")
	service.commitTx = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
		return ackLost
	}
	result, err := service.Apply(ctx, intent)
	var recovery *OperationRecoveryError
	if !errors.As(err, &recovery) || !errors.Is(err, ErrOperationOutcomeUnknown) {
		t.Fatalf("ambiguous rollback error = %v, want recoverable unknown", err)
	}
	if result.Receipt.Status != OperationUnknown || result.Receipt.OperationID != operationID {
		t.Fatalf("ambiguous rollback result = %#v", result)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
	var physicalTable string
	if err := pool.QueryRow(ctx, `SELECT coalesce(to_regclass('app.guarded_unknown')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("ambiguous rollback left DDL %q", physicalTable)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET created_at = current_timestamp - interval '30 seconds',
			updated_at = current_timestamp - interval '20 seconds',
			reconcile_after = current_timestamp - interval '10 seconds'
		WHERE operation_id = $1 AND status = 'unknown'`, operationID); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(pool, &Cache{}, nil)
	processed, err := restarted.reconcileOneSchemaOperation(ctx)
	if err != nil || !processed {
		t.Fatalf("restart reconciliation = processed:%t error:%v", processed, err)
	}
	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := restarted.OperationStatus(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != OperationFailed || reconciled.FailureCode != OperationFailureReconcileFailed {
		t.Fatalf("reconciled ambiguous rollback receipt = %#v", reconciled)
	}
}

func TestGuardedApplyPersistsFailureAfterCallerCancellation(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_canceled"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000661")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "guarded-schema-key-0061")
	if err != nil {
		t.Fatal(err)
	}
	service.beforeDDL = func(callContext context.Context, _ pgx.Tx) error {
		cancel()
		return callContext.Err()
	}

	result, err := service.Apply(ctx, intent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled guarded apply error = %v, want context.Canceled", err)
	}
	if result.Receipt.OperationID != operationID || result.Receipt.Status != OperationFailed ||
		result.Receipt.FailureCode != OperationFailureApplyFailed || result.Receipt.CompletedAt == nil {
		t.Fatalf("canceled guarded receipt = %#v", result.Receipt)
	}
	ref, err := NewOperationRef(administratorID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.OperationStatus(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, result.Receipt) {
		t.Fatalf("stored canceled receipt = %#v, result = %#v", stored, result.Receipt)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
}

func TestClaimFencePreventsAStaleSchemaWriterFromExecutingDDL(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_fenced"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000671")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "guarded-schema-key-0071")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := service.claimOperation(ctx, intent)
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
	current, err := service.claimOperation(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if !current.acquired || current.fenceGeneration != stale.fenceGeneration+1 {
		t.Fatalf("takeover claim = %#v, stale = %#v", current, stale)
	}

	relationOperation := false
	if _, err := service.applyClaimedOperation(ctx, intent, stale, &relationOperation); err == nil {
		t.Fatal("stale fenced writer unexpectedly applied")
	} else {
		var recovery *OperationRecoveryError
		if !errors.As(err, &recovery) {
			t.Fatalf("stale fenced writer error = %v, want recovery error", err)
		}
	}
	assertSchemaCounts(t, pool, schemaCounts{})

	committed, err := service.applyClaimedOperation(ctx, intent, current, &relationOperation)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Receipt.Status != OperationCommitted || committed.Collection.ID != current.row.resourceID {
		t.Fatalf("current fenced writer result = %#v", committed)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, migrations: 1})
}

func TestClaimLockRejectsPersistedRequestHashTamperingBeforeDDL(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "guarded_tampered"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000681")
	intent, err := GuardedApply(change, plan, administratorID, operationID, "guarded-schema-key-0081")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.claimOperation(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET request_hash = decode(repeat('99', 32), 'hex')
		WHERE operation_id = $1`, operationID); err != nil {
		t.Fatal(err)
	}

	relationOperation := false
	_, err = service.applyClaimedOperation(ctx, intent, claim, &relationOperation)
	var recovery *OperationRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("tampered claim error = %v, want recovery error", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
	var physicalTable string
	if err := pool.QueryRow(ctx, `SELECT coalesce(to_regclass('app.guarded_tampered')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("tampered claim executed DDL for %q", physicalTable)
	}
}

func assertExactLedgerAuditLink(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, receipt OperationReceipt) {
	t.Helper()
	var exact bool
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
		  AND operation.status = 'committed'
	)`, receipt.OperationID).Scan(&exact); err != nil {
		t.Fatal(err)
	}
	if !exact {
		t.Fatalf("operation %s did not link to one exact audit receipt", receipt.OperationID)
	}
}
