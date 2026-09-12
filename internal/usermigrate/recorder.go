package usermigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type SourceStatus struct {
	OperationID uuid.UUID
	MigrationID string
	Filename    string
	Status      string
	Attempts    int64
	LastError   string
	CreatedAt   time.Time
	WrittenAt   *time.Time
}

type AutoRecorder struct {
	pool      *database.Pool
	directory string
	logger    *slog.Logger
	interval  time.Duration
}

func NewAutoRecorder(pool *database.Pool, directory string, logger *slog.Logger) (*AutoRecorder, error) {
	if pool == nil {
		return nil, errors.New("automatic user migration pool is nil")
	}
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("automatic user migration directory is empty")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve automatic user migration directory: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AutoRecorder{pool: pool, directory: filepath.Clean(absolute), logger: logger, interval: 2 * time.Second}, nil
}

func (r *AutoRecorder) RecordSchemaMigration(ctx context.Context, tx pgx.Tx, record schema.MigrationRecording) error {
	if r == nil || tx == nil || record.OperationID == uuid.Nil || record.CommittedAt.IsZero() {
		return errors.New("invalid automatic user migration recording")
	}
	name := normalizeRecordedName(record.Name)
	id := record.CommittedAt.UTC().Format("20060102150405") + "_" + name + "_" + strings.ReplaceAll(record.OperationID.String(), "-", "")
	generated, err := GenerateSnapshotSource(id, "migrations", record.Before, record.After)
	if err != nil {
		return err
	}
	revision := sha256.Sum256(generated.Source)
	commandTag, err := tx.Exec(ctx, `insert into pgbase_meta.user_migration_sources(
		operation_id,migration_id,filename,source,revision,status,attempts,created_at,updated_at
	) values($1,$2,$3,$4,$5,'pending',0,$6,$6) on conflict(operation_id) do nothing`,
		record.OperationID, generated.ID, generated.Filename, generated.Source, revision[:], record.CommittedAt.UTC())
	if err != nil {
		return fmt.Errorf("queue automatic user migration source: %w", err)
	}
	if commandTag.RowsAffected() == 1 {
		return nil
	}
	var storedID, storedFilename string
	var storedSource, storedRevision []byte
	if err := tx.QueryRow(ctx, `select migration_id,filename,source,revision from pgbase_meta.user_migration_sources where operation_id=$1`, record.OperationID).Scan(&storedID, &storedFilename, &storedSource, &storedRevision); err != nil {
		return err
	}
	if storedID != generated.ID || storedFilename != generated.Filename || !bytes.Equal(storedSource, generated.Source) || !bytes.Equal(storedRevision, revision[:]) {
		return errors.New("automatic user migration operation conflicts with queued source")
	}
	return nil
}

func normalizeRecordedName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	lastUnderscore := false
	for _, character := range name {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
			lastUnderscore = false
			continue
		}
		if builder.Len() > 0 && !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" || result[0] < 'a' || result[0] > 'z' {
		return "schema_change"
	}
	return result
}

func (r *AutoRecorder) MaterializeSchemaMigration(ctx context.Context, operationID uuid.UUID) error {
	if r == nil || r.pool == nil {
		return errors.New("automatic user migration recorder is unavailable")
	}
	var filename, status string
	var source []byte
	if err := r.pool.QueryRow(ctx, `select filename,source,status from pgbase_meta.user_migration_sources where operation_id=$1`, operationID).Scan(&filename, &source, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if status == "conflict" {
		return errors.New("automatic user migration source has a file conflict")
	}
	err := r.materialize(filename, source)
	if err == nil {
		_, updateErr := r.pool.Exec(ctx, `update pgbase_meta.user_migration_sources set status='written',attempts=attempts+1,last_error=null,written_at=clock_timestamp(),updated_at=clock_timestamp() where operation_id=$1 and status='pending'`, operationID)
		return updateErr
	}
	message := boundedSourceError(err)
	if errors.Is(err, os.ErrExist) {
		_, updateErr := r.pool.Exec(ctx, `update pgbase_meta.user_migration_sources set status='conflict',attempts=attempts+1,last_error=$2,written_at=null,updated_at=clock_timestamp() where operation_id=$1 and status='pending'`, operationID, message)
		if updateErr != nil {
			return updateErr
		}
		return err
	}
	_, updateErr := r.pool.Exec(ctx, `update pgbase_meta.user_migration_sources set attempts=attempts+1,last_error=$2,updated_at=clock_timestamp() where operation_id=$1 and status='pending'`, operationID, message)
	if updateErr != nil {
		return updateErr
	}
	return err
}

func (r *AutoRecorder) materialize(filename string, source []byte) error {
	if filepath.Base(filename) != filename || !strings.HasSuffix(filename, ".go") {
		return errors.New("invalid automatic user migration filename")
	}
	if err := os.MkdirAll(r.directory, 0o755); err != nil {
		return fmt.Errorf("create automatic user migration directory: %w", err)
	}
	path := filepath.Join(r.directory, filename)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: automatic user migration path is not a regular file", os.ErrExist)
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("inspect automatic user migration file: %w", readErr)
		}
		if bytes.Equal(existing, source) {
			return nil
		}
		return fmt.Errorf("%w: automatic user migration file differs", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect automatic user migration file: %w", err)
	}
	temporary, err := os.CreateTemp(r.directory, ".pgbase-migration-*")
	if err != nil {
		return fmt.Errorf("create automatic user migration temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(source); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write automatic user migration source: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync automatic user migration source: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, source) {
				return nil
			}
		}
		return fmt.Errorf("publish automatic user migration source: %w", err)
	}
	directory, err := os.Open(r.directory)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func boundedSourceError(err error) string {
	message := strings.ReplaceAll(err.Error(), "\x00", "")
	if len(message) > 1024 {
		message = message[:1024]
	}
	if message == "" {
		return "source materialization failed"
	}
	return message
}

func (r *AutoRecorder) Status(ctx context.Context) ([]SourceStatus, error) {
	rows, err := r.pool.Query(ctx, `select operation_id,migration_id,filename,status,attempts,coalesce(last_error,''),created_at,written_at from pgbase_meta.user_migration_sources order by created_at,operation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SourceStatus
	for rows.Next() {
		var item SourceStatus
		if err := rows.Scan(&item.OperationID, &item.MigrationID, &item.Filename, &item.Status, &item.Attempts, &item.LastError, &item.CreatedAt, &item.WrittenAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *AutoRecorder) Recover(ctx context.Context) error {
	statuses, err := r.Status(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, status := range statuses {
		if status.Status != "pending" && status.Status != "written" {
			continue
		}
		if err := r.MaterializeSchemaMigration(ctx, status.OperationID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (r *AutoRecorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.Recover(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("recover automatic user migration sources", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
