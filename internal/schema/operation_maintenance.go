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
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	schemaOperationReconcileInterval = time.Second
	schemaOperationReconcileBatch    = 32
	schemaOperationCleanupInterval   = 5 * time.Minute
	schemaOperationCleanupRetention  = 30 * 24 * time.Hour
	schemaOperationCleanupBatch      = 256
	schemaOperationCleanupYield      = 10 * time.Millisecond
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
	if err := s.reconcileSchemaOperations(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
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
			if err := s.reconcileSchemaOperations(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		case <-cleanupTicker.C:
			if err := s.drainSchemaOperationCleanup(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
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
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire schema operation maintenance connection: %w", err)
	}
	defer connection.Release()

	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin schema operation reconciliation: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
		return false, fmt.Errorf("set schema operation reconciliation lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
		return false, fmt.Errorf("disable schema operation reconciliation statement timeout: %w", err)
	}

	var operation maintenanceOperation
	err = tx.QueryRow(ctx, `SELECT operation_id, operation, actor_id, plan_checksum, base_version
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
		cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
		defer cancel()
		if err := tx.Rollback(cleanupCtx); err != nil {
			return false, fmt.Errorf("close empty schema operation reconciliation: %w", err)
		}
		committed = true
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim schema operation reconciliation: %w", err)
	}
	var acquiredSchemaLock bool
	if err := tx.QueryRow(ctx, "select pg_try_advisory_xact_lock($1)", schemaAdvisoryLockKey).Scan(&acquiredSchemaLock); err != nil {
		return false, fmt.Errorf("try schema operation reconciliation advisory lock: %w", err)
	}
	if !acquiredSchemaLock {
		cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
		defer cancel()
		if err := tx.Rollback(cleanupCtx); err != nil {
			return false, fmt.Errorf("defer contended schema operation reconciliation: %w", err)
		}
		committed = true
		return false, nil
	}

	var schemaVersion int64
	if err := tx.QueryRow(ctx, `SELECT version
		FROM pgbase_meta.schema_state
		WHERE singleton
		FOR UPDATE`).Scan(&schemaVersion); err != nil {
		return false, fmt.Errorf("lock schema state for operation reconciliation: %w", err)
	}

	targetVersion := operation.baseVersion + 1
	audits, err := readMaintenanceAudits(ctx, tx, operation.operationID)
	if err != nil {
		return false, err
	}
	switch {
	case len(audits) == 0:
		if err := transitionReconciledOperation(ctx, tx, operation.operationID, OperationFailed); err != nil {
			return false, err
		}
	case len(audits) == 1 && auditExactlyMatchesOperation(audits[0], operation) && schemaVersion >= targetVersion:
		if err := transitionReconciledOperation(ctx, tx, operation.operationID, OperationCommitted); err != nil {
			return false, err
		}
	default:
		return false, errSchemaOperationAuditIntegrity
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit schema operation reconciliation: %w", err)
	}
	committed = true
	return true, nil
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
	var tag interface{ RowsAffected() int64 }
	var err error
	switch status {
	case OperationCommitted:
		tag, err = tx.Exec(ctx, `UPDATE pgbase_meta.schema_operations
			SET status = 'committed',
				lease_owner = NULL,
				lease_expires_at = NULL,
				reconcile_after = NULL,
				committed_version = base_version + 1,
				failure_code = NULL,
				updated_at = current_timestamp,
				completed_at = current_timestamp
			WHERE operation_id = $1
			  AND status IN ('running', 'unknown')`, operationID)
	case OperationFailed:
		tag, err = tx.Exec(ctx, `UPDATE pgbase_meta.schema_operations
			SET status = 'failed',
				lease_owner = NULL,
				lease_expires_at = NULL,
				reconcile_after = NULL,
				committed_version = NULL,
				failure_code = 'reconcile_failed',
				updated_at = current_timestamp,
				completed_at = current_timestamp
			WHERE operation_id = $1
			  AND status IN ('running', 'unknown')`, operationID)
	default:
		return errSchemaOperationAuditIntegrity
	}
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
		timer := time.NewTimer(schemaOperationCleanupYield)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
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
