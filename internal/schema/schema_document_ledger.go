package schema

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) SchemaImportStatus(ctx context.Context, actor, operationID uuid.UUID) (SchemaImportResult, error) {
	var result SchemaImportResult
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select operation_id,status,coalesce(committed_version,0),applied from pgbase_meta.schema_import_operations where operation_id=$1 and administrator_id=$2`, operationID, actor).Scan(&result.OperationID, &result.Status, &result.Version, &result.Applied); err != nil {
			return err
		}
		if result.Status == "running" {
			var orphaned bool
			if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock($1)`, schemaImportLockKey(operationID)).Scan(&orphaned); err != nil {
				return err
			}
			if orphaned {
				result.Status = "unknown"
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrOperationNotFound
	}
	return result, err
}

func schemaImportLockKey(operationID uuid.UUID) int64 {
	sum := sha256.Sum256(append([]byte("pgbase-schema-import:"), operationID[:]...))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// finalizeSchemaImportFailure waits for the exact operation lock before
// changing a receipt. A canceled duplicate can therefore never mark the live
// request it was waiting behind as failed.
func (s *Service) finalizeSchemaImportFailure(parent context.Context, intent SchemaImportIntent, idempotencyHash, requestHash []byte) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	_ = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, schemaImportLockKey(intent.OperationID)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update pgbase_meta.schema_import_operations set status='failed',failure_code='apply_failed',updated_at=clock_timestamp(),completed_at=clock_timestamp() where operation_id=$1 and administrator_id=$2 and idempotency_hash=$3 and request_hash=$4 and status='running'`, intent.OperationID, intent.Actor, idempotencyHash, requestHash)
		return err
	})
}
