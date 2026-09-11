//go:build integration

package usermigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAutoRecorderCommitRecoveryConflictAndOptOut(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	actor := uuid.MustParse("0199ab10-0000-7000-8000-000000000200")
	insertSnapshotAdministrator(t, pool, actor, "recorder@example.test")

	optOut := schema.NewService(pool, &schema.Cache{}, nil)
	applySnapshotChangeForTest(t, optOut, actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "unrecorded"}))
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migration_sources`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("opt-out records=%d err=%v", count, err)
	}

	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not-a-directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder, err := NewAutoRecorder(pool, filepath.Join(blocked, "migrations"), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := schema.NewService(pool, &schema.Cache{}, nil)
	service.SetMigrationRecorder(recorder)
	applySnapshotChangeForTest(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "recorded", Fields: []schema.FieldInput{{Name: "value", Type: schema.TypeText}},
	}))
	statuses, err := recorder.Status(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].Status != "pending" || statuses[0].Attempts != 1 {
		t.Fatalf("pending statuses=%+v err=%v", statuses, err)
	}
	if !bytes.Contains([]byte(statuses[0].LastError), []byte("directory")) {
		t.Fatalf("pending error=%q", statuses[0].LastError)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err = recorder.Status(ctx)
	if err != nil || statuses[0].Status != "written" || statuses[0].Attempts != 2 {
		t.Fatalf("recovered statuses=%+v err=%v", statuses, err)
	}
	path := filepath.Join(recorder.directory, statuses[0].Filename)
	source, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(source, []byte("ApplyUserMigrationSnapshot")) || bytes.Contains(source, []byte("recorder@example.test")) {
		t.Fatalf("materialized source valid=%t secret=%t err=%v", bytes.Contains(source, []byte("ApplyUserMigrationSnapshot")), bytes.Contains(source, []byte("recorder@example.test")), err)
	}

	before := exportSnapshot(t, pool)
	after := before
	operationID := uuid.MustParse("0199ab10-0000-7000-8000-000000000201")
	rollbackErr := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := recorder.RecordSchemaMigration(ctx, tx, schema.MigrationRecording{OperationID: operationID, Name: "rolled_back", Before: before, After: after, CommittedAt: databaseRecordingTime(t, pool)}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if rollbackErr == nil {
		t.Fatal("record transaction did not roll back")
	}
	if err := recorder.MaterializeSchemaMigration(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migration_sources where operation_id=$1`, operationID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back record count=%d err=%v", count, err)
	}

	conflictID := uuid.MustParse("0199ab10-0000-7000-8000-000000000202")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return recorder.RecordSchemaMigration(ctx, tx, schema.MigrationRecording{OperationID: conflictID, Name: "conflict", Before: before, After: after, CommittedAt: databaseRecordingTime(t, pool)})
	}); err != nil {
		t.Fatal(err)
	}
	statuses, _ = recorder.Status(ctx)
	var conflict SourceStatus
	for _, status := range statuses {
		if status.OperationID == conflictID {
			conflict = status
		}
	}
	conflictPath := filepath.Join(recorder.directory, conflict.Filename)
	if err := os.WriteFile(conflictPath, []byte("owned by user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recorder.MaterializeSchemaMigration(ctx, conflictID); !errors.Is(err, os.ErrExist) {
		t.Fatalf("conflict error=%v", err)
	}
	if got, err := os.ReadFile(conflictPath); err != nil || string(got) != "owned by user" {
		t.Fatalf("conflict file=%q err=%v", got, err)
	}
}

func TestAutoRecorderRecordsSchemaImport(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	actor := uuid.MustParse("0199ab10-0000-7000-8000-000000000212")
	insertSnapshotAdministrator(t, pool, actor, "import-recorder@example.test")
	recorder, err := NewAutoRecorder(pool, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := schema.NewService(pool, &schema.Cache{}, nil)
	service.SetMigrationRecorder(recorder)
	document := schema.SchemaDocument{Format: schema.SchemaDocumentFormat, Collections: []schema.SchemaDocumentCollection{{
		Name: "import_recorded", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}, Rules: json.RawMessage(`{}`),
	}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.MustParse("0199ab10-0000-7000-8000-000000000213")
	if _, err := service.ApplySchemaImport(ctx, schema.SchemaImportIntent{
		Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor,
		OperationID: operationID, IdempotencyKey: "n1-import-recording-0001",
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err := recorder.Status(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].OperationID != operationID || statuses[0].Status != "written" {
		t.Fatalf("schema import statuses=%+v err=%v", statuses, err)
	}
}

func TestAutoRecorderConcurrentMaterializeWritesOneSource(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	recorder, err := NewAutoRecorder(pool, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := exportSnapshot(t, pool)
	operationID := uuid.MustParse("0199ab10-0000-7000-8000-000000000211")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return recorder.RecordSchemaMigration(ctx, tx, schema.MigrationRecording{OperationID: operationID, Name: "concurrent", Before: snapshot, After: snapshot, CommittedAt: databaseRecordingTime(t, pool)})
	}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsFound <- recorder.MaterializeSchemaMigration(ctx, operationID)
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	statuses, err := recorder.Status(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].Status != "written" {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	files, err := filepath.Glob(filepath.Join(recorder.directory, "*.go"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

func TestAutoRecorderRecoverRestoresMissingWrittenSource(t *testing.T) {
	ctx := context.Background()
	pool := userMigrationPool(t)
	recorder, err := NewAutoRecorder(pool, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := exportSnapshot(t, pool)
	operationID := uuid.MustParse("0199ab10-0000-7000-8000-000000000214")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return recorder.RecordSchemaMigration(ctx, tx, schema.MigrationRecording{OperationID: operationID, Name: "restore_missing", Before: snapshot, After: snapshot, CommittedAt: databaseRecordingTime(t, pool)})
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.MaterializeSchemaMigration(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	statuses, err := recorder.Status(ctx)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	path := filepath.Join(recorder.directory, statuses[0].Filename)
	var stored []byte
	if err := pool.QueryRow(ctx, `select source from pgbase_meta.user_migration_sources where operation_id=$1`, operationID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, stored) {
		t.Fatalf("restored exact stored source=%t err=%v", bytes.Equal(restored, stored), err)
	}
	if err := os.WriteFile(path, []byte("user-owned replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Recover(ctx); !errors.Is(err, os.ErrExist) {
		t.Fatalf("differing written source recovery error=%v", err)
	}
	if existing, err := os.ReadFile(path); err != nil || string(existing) != "user-owned replacement" {
		t.Fatalf("differing file overwritten=%q err=%v", existing, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Recover(ctx); !errors.Is(err, os.ErrExist) {
		t.Fatalf("non-regular written source recovery error=%v", err)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		t.Fatalf("non-regular path replaced: info=%v err=%v", info, err)
	}
}

func databaseRecordingTime(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) time.Time {
	t.Helper()
	var value time.Time
	if err := pool.QueryRow(context.Background(), `select clock_timestamp()`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value.UTC()
}
