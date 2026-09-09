package schema

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type MigrationRecording struct {
	OperationID uuid.UUID
	Name        string
	Before      []byte
	After       []byte
	CommittedAt time.Time
}

// MigrationRecorder durably queues source in the schema transaction, then
// materializes it only after commit. Materialize errors remain retryable state.
type MigrationRecorder interface {
	RecordSchemaMigration(context.Context, pgx.Tx, MigrationRecording) error
	MaterializeSchemaMigration(context.Context, uuid.UUID) error
}

func (s *Service) SetMigrationRecorder(recorder MigrationRecorder) {
	if s != nil {
		s.migrationRecorder = recorder
	}
}

func (s *Service) materializeSchemaMigration(ctx context.Context, operationID uuid.UUID) {
	if s == nil || s.migrationRecorder == nil || operationID == uuid.Nil {
		return
	}
	_ = s.migrationRecorder.MaterializeSchemaMigration(context.WithoutCancel(ctx), operationID)
}
