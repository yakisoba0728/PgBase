package system_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestHistoricalMigrationChecksumsRemainImmutable(t *testing.T) {
	want := map[int64]string{
		1: "c1fc7648bae60359cb3adcb11feae210b02b54a79fd5933abfb1cd3e2736fca4",
		2: "fe3eebdc35d995593d3185169e61670b37a1c5906d06562649a00c50448c32fb",
		3: "effa5a5895a8ec387c73b4a0afdb1ed3aeb154f09f3d0825862f00d6237c1303",
		4: "747f4377b19f70e2bdeaf77e5ee9b96f0bb019f0ba0a6d5f7d856d8adb90751b",
		5: "576df4a578efbd1259cf5dbf22da8d7669bd729e33d2ef1d1b4ed30e1d16197a",
		6: "6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a",
	}
	migrations := system.Migrations()
	if len(migrations) != 6 || migrations[5].Version != 6 || migrations[5].Name != "schema_operations" {
		t.Fatalf("migration ordering=%+v", migrations)
	}
	// Every applied migration is pinned, so adding one without its checksum
	// fails here instead of at the runner of an already migrated database.
	if len(want) != len(migrations) {
		t.Fatalf("pinned checksums=%d migrations=%d", len(want), len(migrations))
	}
	for _, migration := range migrations {
		if got := hex.EncodeToString(migration.SHA256[:]); got != want[migration.Version] {
			t.Fatalf("historical migration %d checksum=%s want=%s", migration.Version, got, want[migration.Version])
		}
	}
	copyOfMigrations := system.Migrations()
	copyOfMigrations[0].Name = "changed"
	if system.Migrations()[0].Name != "initial" {
		t.Fatal("Migrations returned mutable ordering state")
	}
}

func newRecordOperationsSchema(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range system.Migrations() {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
	return ctx, pool
}

func insertConsistentRecordArtifacts(t *testing.T, ctx context.Context, pool *database.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000100', 'records', 'records',
		 'active', transaction_timestamp(), transaction_timestamp());

		INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
		 record_id, operation, schema_version, schema_digest, previous_version,
		 new_version, previous_etag, new_etag, changed_fields, input_hash,
		 idempotency_key_hash, compatibility, committed_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000102', 'administrator',
		 '019551d0-e9c0-7000-8000-000000000103', 'request01',
		 '019551d0-e9c0-7000-8000-000000000100', 'records',
		 '019551d0-e9c0-7000-8000-000000000101', 'create', 1,
		 decode(repeat('aa', 32), 'hex'), NULL, 1, NULL, '"pgb1.test"',
		 ARRAY['title'], decode(repeat('bb', 32), 'hex'),
		 decode(repeat('cc', 32), 'hex'), 'none', transaction_timestamp());

		INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000100',
		 '019551d0-e9c0-7000-8000-000000000101', 1,
		 decode(repeat('dd', 32), 'hex'),
		 '019551d0-e9c0-7000-8000-000000000102', transaction_timestamp());

		INSERT INTO pgbase_meta.record_outbox
		(event_id, topic, collection_id, collection_name, record_id, operation,
		 record_version, envelope, created_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000102', 'record.mutation.v1',
		 '019551d0-e9c0-7000-8000-000000000100', 'records',
		 '019551d0-e9c0-7000-8000-000000000101', 'create', 1,
		 ('{"version":1,"eventId":"019551d0-e9c0-7000-8000-000000000102",' ||
		 '"collectionId":"019551d0-e9c0-7000-8000-000000000100",' ||
		 '"collectionName":"records",' ||
		 '"recordId":"019551d0-e9c0-7000-8000-000000000101",' ||
		 '"operation":"create","recordVersion":1,"changes":{"title":"ok"}}')::jsonb,
		 transaction_timestamp());

		INSERT INTO pgbase_meta.record_idempotency
		(actor_kind, actor_id, collection_id, key_hash, request_hash, record_id,
		 event_id, response_body, etag, created_at, expires_at)
		VALUES ('administrator', '019551d0-e9c0-7000-8000-000000000103',
		 '019551d0-e9c0-7000-8000-000000000100', decode(repeat('cc', 32), 'hex'),
		 decode(repeat('bb', 32), 'hex'), '019551d0-e9c0-7000-8000-000000000101',
		 '019551d0-e9c0-7000-8000-000000000102',
		 '{"id":"019551d0-e9c0-7000-8000-000000000101","title":"ok"}',
		 '"pgb1.test"', transaction_timestamp(), transaction_timestamp() + interval '24 hours')`)
	if err != nil {
		t.Fatalf("insert consistent record artifacts: %v", err)
	}
}

func requireSQLState(t *testing.T, ctx context.Context, pool *database.Pool, statement, want string) {
	t.Helper()
	_, err := pool.Exec(ctx, statement)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		t.Fatalf("statement error=%v SQLSTATE=%q want=%q", err, postgresErrorCode(postgresError), want)
	}
}

func postgresErrorCode(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.Code
}
