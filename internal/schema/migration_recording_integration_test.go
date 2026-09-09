//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type transactionalMigrationRecorder struct {
	materializeCalls int
}

func (r *transactionalMigrationRecorder) RecordSchemaMigration(ctx context.Context, tx pgx.Tx, recording MigrationRecording) error {
	_, err := tx.Exec(ctx, `insert into public.n1_recorded_transactions(operation_id) values($1)`, recording.OperationID)
	return err
}

func (r *transactionalMigrationRecorder) MaterializeSchemaMigration(context.Context, uuid.UUID) error {
	r.materializeCalls++
	return nil
}

func TestAutomaticMigrationRecordRollsBackAndDoesNotMaterializeOnCommitFailure(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `create table public.n1_recorded_transactions(operation_id uuid primary key)`); err != nil {
		t.Fatal(err)
	}
	recorder := &transactionalMigrationRecorder{}
	service.SetMigrationRecorder(recorder)
	change := NewCreateChange(CreateCollectionInput{Name: "commit_failure_recording", Fields: []FieldInput{{Name: "title", Type: TypeText}}})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	service.commitTx = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
		return pgx.ErrTxCommitRollback
	}
	if _, err := service.Apply(ctx, LegacyApply(change, plan, actor)); err == nil || !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("commit failure error=%v", err)
	}
	var recordCount int
	if err := pool.QueryRow(ctx, `select count(*) from public.n1_recorded_transactions`).Scan(&recordCount); err != nil {
		t.Fatal(err)
	}
	if recordCount != 0 || recorder.materializeCalls != 0 {
		t.Fatalf("commit failure records=%d materialize=%d", recordCount, recorder.materializeCalls)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
}
