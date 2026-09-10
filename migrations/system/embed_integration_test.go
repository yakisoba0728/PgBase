//go:build integration

package system_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestRelationSchemaMigrationUpgradesPopulatedMaximumColumnTableWithoutAlteringIt(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrations := system.Migrations()
	for _, migration := range migrations[:4] {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}

	columns := []string{
		`"id" uuid PRIMARY KEY`,
		`"created_at" timestamptz NOT NULL`,
		`"updated_at" timestamptz NOT NULL`,
	}
	for index := 0; index < 1597; index++ {
		columns = append(columns, fmt.Sprintf(`"field_%04d" boolean`, index))
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE app.maximum_fields ("+strings.Join(columns, ",")+")"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app.maximum_fields (id, created_at, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000099', transaction_timestamp(), transaction_timestamp());
		INSERT INTO pgbase_meta.collections (id, name, table_name, status, created_at, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000090', 'maximum_fields', 'maximum_fields', 'active', transaction_timestamp(), transaction_timestamp());
		INSERT INTO pgbase_meta.fields
			(id, collection_id, name, column_name, field_type, required, is_unique,
			 is_indexed, has_default, default_json, ordinal, created_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000091',
			'019551d0-e9c0-7000-8000-000000000090', 'field_0000', 'field_0000',
			'boolean', false, false, false, false, NULL, 0, transaction_timestamp())`); err != nil {
		t.Fatal(err)
	}
	var beforeColumns, beforeRows int
	var beforeOID int64
	var beforeSignature string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM information_schema.columns
		 WHERE table_schema='app' AND table_name='maximum_fields'),
		'app.maximum_fields'::regclass::oid::bigint,
		(SELECT count(*) FROM app.maximum_fields),
		(SELECT jsonb_agg(jsonb_build_object(
			'position', ordinal_position,
			'name', column_name,
			'type', data_type,
			'udt', udt_name,
			'nullable', is_nullable,
			'default', column_default
		) ORDER BY ordinal_position)::text
		 FROM information_schema.columns
		 WHERE table_schema='app' AND table_name='maximum_fields')`).Scan(
		&beforeColumns, &beforeOID, &beforeRows, &beforeSignature,
	); err != nil {
		t.Fatal(err)
	}
	if beforeColumns != 1600 || beforeRows != 1 {
		t.Fatalf("pre-migration columns=%d rows=%d want=1600/1", beforeColumns, beforeRows)
	}
	if _, err := pool.Exec(ctx, migrations[4].SQL); err != nil {
		t.Fatalf("apply relation schema migration: %v", err)
	}
	var afterColumns, afterRows, metadataFields int
	var afterOID int64
	var afterSignature string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM information_schema.columns
		 WHERE table_schema='app' AND table_name='maximum_fields'),
		'app.maximum_fields'::regclass::oid::bigint,
		(SELECT count(*) FROM app.maximum_fields),
		(SELECT jsonb_agg(jsonb_build_object(
			'position', ordinal_position,
			'name', column_name,
			'type', data_type,
			'udt', udt_name,
			'nullable', is_nullable,
			'default', column_default
		) ORDER BY ordinal_position)::text
		 FROM information_schema.columns
		 WHERE table_schema='app' AND table_name='maximum_fields'),
		(SELECT count(*) FROM pgbase_meta.fields
		 WHERE id='019551d0-e9c0-7000-8000-000000000091' AND field_type='boolean')`).Scan(
		&afterColumns, &afterOID, &afterRows, &afterSignature, &metadataFields,
	); err != nil {
		t.Fatal(err)
	}
	if afterColumns != beforeColumns || afterOID != beforeOID || afterRows != beforeRows || afterSignature != beforeSignature {
		t.Fatalf("relation schema migration changed dynamic table: columns %d->%d oid %d->%d rows %d->%d signature_equal=%t",
			beforeColumns, afterColumns, beforeOID, afterOID, beforeRows, afterRows, afterSignature == beforeSignature)
	}
	if metadataFields != 1 {
		t.Fatalf("preexisting scalar field metadata rows=%d want=1", metadataFields)
	}
	var sidecarTables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema='pgbase_meta' AND table_name IN
		('record_revisions','record_audit_events','record_outbox','record_idempotency')`).Scan(&sidecarTables); err != nil {
		t.Fatal(err)
	}
	if sidecarTables != 4 {
		t.Fatalf("record operation sidecar table count=%d want=4", sidecarTables)
	}
}

func TestRecordOperationsMigrationPreservesConsistentArtifactsAfterCollectionDelete(t *testing.T) {
	ctx, pool := newRecordOperationsSchema(t)
	insertConsistentRecordArtifacts(t, ctx, pool)

	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.collections
		WHERE id = '019551d0-e9c0-7000-8000-000000000100'`); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{
		"pgbase_meta.record_revisions":    0,
		"pgbase_meta.record_audit_events": 1,
		"pgbase_meta.record_outbox":       1,
		"pgbase_meta.record_idempotency":  1,
	} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s rows after collection delete=%d want=%d", table, count, want)
		}
	}
}

func TestRecordOperationsMigrationRejectsImpossibleCommittedArtifacts(t *testing.T) {
	ctx, pool := newRecordOperationsSchema(t)
	insertConsistentRecordArtifacts(t, ctx, pool)

	t.Run("operation shape", func(t *testing.T) {
		requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.record_audit_events
			(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
			 record_id, operation, schema_version, schema_digest, previous_version,
			 new_version, previous_etag, new_etag, changed_fields, input_hash,
			 idempotency_key_hash, compatibility, committed_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000104', 'administrator',
			 '019551d0-e9c0-7000-8000-000000000103', 'request02',
			 '019551d0-e9c0-7000-8000-000000000100', 'records',
			 '019551d0-e9c0-7000-8000-000000000105', 'delete', 1,
			 decode(repeat('aa', 32), 'hex'), NULL, 9, NULL, '"pgb1.invalid"',
			 ARRAY['title'], decode(repeat('bb', 32), 'hex'),
			 decode(repeat('cc', 32), 'hex'), 'missing-idempotency-key',
			 transaction_timestamp())`, "23514")
	})

	t.Run("revision event", func(t *testing.T) {
		requireSQLState(t, ctx, pool, `INSERT INTO pgbase_meta.record_revisions
			(collection_id, record_id, version, validator, last_event_id, updated_at)
			VALUES ('019551d0-e9c0-7000-8000-000000000100',
			 '019551d0-e9c0-7000-8000-000000000199', 42,
			 decode(repeat('dd', 32), 'hex'),
			 '019551d0-e9c0-7000-8000-000000000102', transaction_timestamp())`, "23514")
	})

	t.Run("outbox event", func(t *testing.T) {
		requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_outbox
			SET record_version = 2,
				envelope = jsonb_set(envelope, '{recordVersion}', '2'::jsonb)
			WHERE event_id = '019551d0-e9c0-7000-8000-000000000102'`, "23514")
	})

	t.Run("outbox envelope", func(t *testing.T) {
		requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_outbox
			SET envelope = '[]'::jsonb
			WHERE event_id = '019551d0-e9c0-7000-8000-000000000102'`, "23514")
	})

	t.Run("idempotency event", func(t *testing.T) {
		requireSQLState(t, ctx, pool, `UPDATE pgbase_meta.record_idempotency
			SET actor_id = '019551d0-e9c0-7000-8000-000000000198'
			WHERE event_id = '019551d0-e9c0-7000-8000-000000000102'`, "23514")
	})

	t.Run("audit truncate", func(t *testing.T) {
		requireSQLState(t, ctx, pool,
			`TRUNCATE TABLE pgbase_meta.record_audit_events CASCADE`, "55000")
	})
}

// applyMigrationsBefore installs every embedded migration below version so a
// test can exercise one migration against the exact history that precedes it.
func applyMigrationsBefore(t *testing.T, ctx context.Context, pool *database.Pool, version int64) {
	t.Helper()
	for _, migration := range system.Migrations() {
		if migration.Version >= version {
			return
		}
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
}

func migrationSQL(t *testing.T, version int64) string {
	t.Helper()
	for _, migration := range system.Migrations() {
		if migration.Version == version {
			return migration.SQL
		}
	}
	t.Fatalf("embedded migration %d not found", version)
	return ""
}
