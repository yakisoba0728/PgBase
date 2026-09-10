package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const (
	schemaOperationReconcileInterval   = time.Second
	schemaOperationReconcileBatch      = 32
	schemaOperationCleanupInterval     = 5 * time.Minute
	schemaOperationCleanupRetention    = 30 * 24 * time.Hour
	schemaOperationCleanupBatch        = 256
	schemaOperationCleanupYield        = 10 * time.Millisecond
	schemaOperationMaintenanceRetryMin = 100 * time.Millisecond
	schemaOperationMaintenanceRetryMax = 5 * time.Second
)

var (
	errSchemaOperationMaintenanceUnavailable = errors.New("schema operation maintenance is unavailable")
	errSchemaOperationAuditIntegrity         = errors.New("schema operation audit integrity violation")
)

// Run owns the schema-operation reconciliation and receipt-retention loop.
// It does not start a goroutine: application lifecycle code owns this call and
// its context. Cancellation is a normal shutdown.
func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.pool == nil || ctx == nil {
		return errSchemaOperationMaintenanceUnavailable
	}
	run := func(operation func(context.Context) error) error {
		err := s.runSchemaOperationMaintenance(ctx, operation)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if err := run(s.reconcileSchemaOperations); err != nil {
		return err
	}

	reconcileTicker := time.NewTicker(schemaOperationReconcileInterval)
	cleanupTicker := time.NewTicker(schemaOperationCleanupInterval)
	defer reconcileTicker.Stop()
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reconcileTicker.C:
			if err := run(s.reconcileSchemaOperations); err != nil {
				return err
			}
		case <-cleanupTicker.C:
			if err := run(s.drainSchemaOperationCleanup); err != nil {
				return err
			}
		}
	}
}

// runSchemaOperationMaintenance keeps a temporary PostgreSQL outage inside the
// readiness boundary instead of turning one failed maintenance attempt into a
// process shutdown. Integrity and programming errors still escape immediately:
// retrying those would only hide a receipt state the operator must investigate.
func (s *Service) runSchemaOperationMaintenance(ctx context.Context, operation func(context.Context) error) error {
	retryMin, retryMax := s.maintenanceRetryMin, s.maintenanceRetryMax
	if retryMin <= 0 {
		retryMin = schemaOperationMaintenanceRetryMin
	}
	if retryMax < retryMin {
		retryMax = retryMin
	}
	for delay := retryMin; ; delay = min(delay*2, retryMax) {
		err := operation(ctx)
		if err == nil || database.Classify(err) != database.KindUnavailable {
			return err
		}
		if err := waitForContext(ctx, delay); err != nil {
			return err
		}
	}
}

func (s *Service) reconcileSchemaOperations(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errSchemaOperationMaintenanceUnavailable
	}
	processed := 0
	for processed < schemaOperationReconcileBatch {
		reconciled, err := s.reconcileOneSchemaOperation(ctx)
		if err != nil {
			if schemaOperationMaintenanceContention(err) {
				return nil
			}
			return err
		}
		if !reconciled {
			return nil
		}
		processed++
	}
	return nil
}

type maintenanceOperation struct {
	operationID uuid.UUID
	operation   SchemaOperation
	actorID     uuid.UUID
	checksum    []byte
	baseVersion int64
}

type maintenanceAudit struct {
	version     int64
	operationID uuid.UUID
	operation   SchemaOperation
	checksum    []byte
	actorID     uuid.UUID
}

func (s *Service) reconcileOneSchemaOperation(ctx context.Context) (reconciled bool, err error) {
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema operation reconciliation lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return fmt.Errorf("disable schema operation reconciliation statement timeout: %w", err)
		}

		var operation maintenanceOperation
		err := tx.QueryRow(ctx, `SELECT operation_id, operation, actor_id, plan_checksum, base_version
		FROM pgbase_meta.schema_operations
		WHERE status IN ('running', 'unknown')
		  AND reconcile_after <= current_timestamp
		ORDER BY reconcile_after, operation_id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(
			&operation.operationID,
			&operation.operation,
			&operation.actorID,
			&operation.checksum,
			&operation.baseVersion,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim schema operation reconciliation: %w", err)
		}
		var acquiredSchemaLock bool
		if err := tx.QueryRow(ctx, "select pg_try_advisory_xact_lock($1)", schemaAdvisoryLockKey).Scan(&acquiredSchemaLock); err != nil {
			return fmt.Errorf("try schema operation reconciliation advisory lock: %w", err)
		}
		if !acquiredSchemaLock {
			return nil
		}

		var schemaVersion int64
		if err := tx.QueryRow(ctx, `SELECT version
		FROM pgbase_meta.schema_state
		WHERE singleton
		FOR UPDATE`).Scan(&schemaVersion); err != nil {
			return fmt.Errorf("lock schema state for operation reconciliation: %w", err)
		}

		audits, err := readMaintenanceAudits(ctx, tx, operation.operationID)
		if err != nil {
			return err
		}
		status := OperationFailed
		switch {
		case len(audits) == 0:
		case len(audits) == 1 && auditExactlyMatchesOperation(audits[0], operation) && schemaVersion >= operation.baseVersion+1:
			status = OperationCommitted
		default:
			return errSchemaOperationAuditIntegrity
		}
		if err := transitionReconciledOperation(ctx, tx, operation.operationID, status); err != nil {
			return err
		}
		reconciled = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return reconciled, nil
}

func readMaintenanceAudits(ctx context.Context, tx pgx.Tx, operationID uuid.UUID) ([]maintenanceAudit, error) {
	rows, err := tx.Query(ctx, `SELECT version, operation_id, operation, checksum, applied_by
		FROM pgbase_meta.schema_migrations
		WHERE operation_id = $1
		ORDER BY version`, operationID)
	if err != nil {
		return nil, fmt.Errorf("read schema operation audit: %w", err)
	}
	defer rows.Close()

	audits := make([]maintenanceAudit, 0, 2)
	for rows.Next() {
		var audit maintenanceAudit
		if err := rows.Scan(&audit.version, &audit.operationID, &audit.operation, &audit.checksum, &audit.actorID); err != nil {
			return nil, fmt.Errorf("scan schema operation audit: %w", err)
		}
		audits = append(audits, audit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema operation audits: %w", err)
	}
	return audits, nil
}

func schemaOperationMaintenanceContention(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "55P03"
}

func auditExactlyMatchesOperation(audit maintenanceAudit, operation maintenanceOperation) bool {
	return audit.version == operation.baseVersion+1 &&
		audit.operationID == operation.operationID &&
		audit.operation == operation.operation &&
		bytes.Equal(audit.checksum, operation.checksum) &&
		audit.actorID == operation.actorID
}

func transitionReconciledOperation(ctx context.Context, tx pgx.Tx, operationID uuid.UUID, status OperationStatus) error {
	if status != OperationCommitted && status != OperationFailed {
		return errSchemaOperationAuditIntegrity
	}
	tag, err := tx.Exec(ctx, `UPDATE pgbase_meta.schema_operations
		SET status = $2,
			lease_owner = NULL,
			lease_expires_at = NULL,
			reconcile_after = NULL,
			committed_version = CASE WHEN $2 = 'committed' THEN base_version + 1 END,
			failure_code = CASE WHEN $2 = 'failed' THEN 'reconcile_failed' END,
			updated_at = current_timestamp,
			completed_at = current_timestamp
		WHERE operation_id = $1
		  AND status IN ('running', 'unknown')`, operationID, status)
	if err != nil {
		return fmt.Errorf("record reconciled schema operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errSchemaOperationAuditIntegrity
	}
	return nil
}

func (s *Service) drainSchemaOperationCleanup(ctx context.Context) error {
	for {
		deleted, err := s.cleanupSchemaOperations(ctx)
		if err != nil {
			return err
		}
		if deleted < schemaOperationCleanupBatch {
			return nil
		}
		if waitForContext(ctx, schemaOperationCleanupYield) != nil {
			return nil
		}
	}
}

func (s *Service) cleanupSchemaOperations(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, errSchemaOperationMaintenanceUnavailable
	}
	tag, err := s.pool.Exec(ctx, `WITH expired AS MATERIALIZED (
		SELECT ctid
		FROM pgbase_meta.schema_operations
		WHERE status IN ('committed', 'failed')
		  AND completed_at < current_timestamp - ($1::bigint * interval '1 second')
		ORDER BY completed_at, operation_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	)
	DELETE FROM pgbase_meta.schema_operations AS operation
	USING expired
	WHERE operation.ctid = expired.ctid`, int64(schemaOperationCleanupRetention/time.Second), schemaOperationCleanupBatch)
	if err != nil {
		return 0, fmt.Errorf("clean schema operation receipts: %w", err)
	}
	return tag.RowsAffected(), nil
}
