//go:build integration

package query

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func waitForGetSnapshotHydrationLock(t *testing.T, observer *pgx.Conn, relationTable string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		queryCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		err := observer.QueryRow(queryCtx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND position($1 in query) > 0`, relationTable).Scan(&waiters)
		cancel()
		if err != nil {
			t.Fatalf("read blocked Get hydration: %v", err)
		}
		if waiters == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Get did not reach relation hydration after reading its root")
}

func TestGetReadsBodyRevisionAndETagFromOneSnapshot(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	observer, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(ctx) })
	target := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "get_snapshot_targets",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	maximumLinks := 2
	collection := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "get_snapshot_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: target.Name, Cardinality: schema.RelationMultiple,
				MaxSelect: &maximumLinks, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	relationTableName := schema.NewRelationPhysicalNames(collection.TableName, "links").MultiTable
	relationTable := pgx.Identifier{"pgbase_relation", relationTableName}.Sanitize()
	recordID := uuid.MustParse("01999111-1111-7111-8111-111111111111")
	firstValidator := bytes.Repeat([]byte{1}, 32)
	secondValidator := bytes.Repeat([]byte{2}, 32)
	digest, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}
	wantFirstETag := recordartifact.RevisionETag(collection.ID, recordID, digest, firstValidator)
	wantSecondETag := recordartifact.RevisionETag(collection.ID, recordID, digest, secondValidator)
	catalogSnapshot, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	administratorID := uuid.MustParse(r0AdministratorID)
	firstEventID := uuid.MustParse("01999111-1111-7111-8111-111111111112")
	secondEventID := uuid.MustParse("01999111-1111-7111-8111-111111111113")
	if _, err := pool.Exec(ctx, `INSERT INTO `+quotedTable(collection)+` (id, created_at, updated_at, title)
		VALUES ($1, transaction_timestamp(), transaction_timestamp(), 'before')`, recordID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name, record_id,
		 operation, schema_version, schema_digest, previous_version, new_version, previous_etag,
		 new_etag, changed_fields, input_hash, idempotency_key_hash, compatibility, committed_at)
		VALUES ($1, 'administrator', $2, 'snapshot_create', $3, $4, $5,
		 'create', $6, $7, NULL, 1, NULL, $8, ARRAY['title'], $9, NULL,
		 'missing-idempotency-key', transaction_timestamp())`,
		firstEventID, administratorID, collection.ID, collection.Name, recordID,
		catalogSnapshot.Version, digest[:], wantFirstETag, bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ($1, $2, 1, $3, $4, transaction_timestamp())`,
		collection.ID, recordID, firstValidator, firstEventID); err != nil {
		t.Fatal(err)
	}

	request := GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)},
		Collection: collection.Name,
		ID:         recordID,
	}
	operationCtx, cancelOperations := context.WithCancel(ctx)
	t.Cleanup(cancelOperations)

	writerReady := make(chan struct{})
	writerRelease := make(chan struct{})
	writerDone := make(chan error, 1)
	var releaseWriterOnce sync.Once
	releaseWriter := func() { releaseWriterOnce.Do(func() { close(writerRelease) }) }
	t.Cleanup(releaseWriter)
	go func() {
		writerDone <- pool.InTx(operationCtx, pgx.TxOptions{}, func(writer pgx.Tx) error {
			if _, err := writer.Exec(operationCtx, "LOCK TABLE "+relationTable+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				return err
			}
			close(writerReady)
			<-writerRelease
			if _, err := writer.Exec(operationCtx, `UPDATE `+quotedTable(collection)+`
				SET title = 'after', updated_at = transaction_timestamp() WHERE id = $1`, recordID); err != nil {
				return err
			}
			if _, err := writer.Exec(operationCtx, `INSERT INTO pgbase_meta.record_audit_events
				(event_id, actor_kind, actor_id, request_id, collection_id, collection_name, record_id,
				 operation, schema_version, schema_digest, previous_version, new_version, previous_etag,
				 new_etag, changed_fields, input_hash, idempotency_key_hash, compatibility, committed_at)
				VALUES ($1, 'administrator', $2, 'snapshot_update', $3, $4, $5,
				 'update', $6, $7, 1, 2, $8, $9, ARRAY['title'], $10, NULL,
				 'missing-if-match', transaction_timestamp())`,
				secondEventID, administratorID, collection.ID, collection.Name, recordID,
				catalogSnapshot.Version, digest[:], wantFirstETag, wantSecondETag, bytes.Repeat([]byte{4}, 32)); err != nil {
				return err
			}
			_, err := writer.Exec(operationCtx, `UPDATE pgbase_meta.record_revisions
				SET version = 2, validator = $3, last_event_id = $4, updated_at = transaction_timestamp()
				WHERE collection_id = $1 AND record_id = $2`, collection.ID, recordID, secondValidator,
				secondEventID)
			return err
		})
	}()

	select {
	case <-writerReady:
	case err := <-writerDone:
		t.Fatalf("lock relation table before Get: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not lock the relation table")
	}

	type readResult struct {
		record Record
		err    error
	}
	readDone := make(chan readResult, 1)
	go func() {
		record, err := records.Get(operationCtx, request)
		readDone <- readResult{record: record, err: err}
	}()

	waitForGetSnapshotHydrationLock(t, observer, relationTableName)
	releaseWriter()
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		cancelOperations()
		t.Fatal("snapshot writer did not finish")
	}
	var snapshot readResult
	select {
	case snapshot = <-readDone:
	case <-time.After(5 * time.Second):
		cancelOperations()
		t.Fatal("Get did not finish after relation hydration resumed")
	}
	if snapshot.err != nil {
		t.Fatal(snapshot.err)
	}

	if snapshot.record.Data["title"] != "before" || snapshot.record.Version != 1 || snapshot.record.ETag != wantFirstETag {
		t.Fatalf("snapshot Get = %#v, want before/version 1/%s", snapshot.record, wantFirstETag)
	}
	latest, err := records.Get(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Data["title"] != "after" || latest.Version != 2 || latest.ETag != wantSecondETag {
		t.Fatalf("latest Get = %#v, want after/version 2/%s", latest, wantSecondETag)
	}
}
