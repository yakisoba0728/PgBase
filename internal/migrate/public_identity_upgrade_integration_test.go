//go:build integration

package migrate

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestPublicIdentityUpgradePreservesPopulatedV9DatabaseAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	migrations := system.Migrations()
	if len(migrations) < 11 || migrations[8].Version != 9 || migrations[9].Version != 10 || migrations[10].Version != 11 {
		t.Fatalf("public identity migration manifest = %#v", migrations)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	legacyRunner := New(pool, logger)
	legacyRunner.migrations = migrations[:9]
	legacyRunner.compatibility = nil
	if err := legacyRunner.Up(ctx); err != nil {
		t.Fatalf("create v9 database: %v", err)
	}

	const (
		adminID      = "019551d0-e9c0-7000-8000-000000000901"
		collectionID = "019551d0-e9c0-7000-8000-000000000902"
		fieldID      = "019551d0-e9c0-7000-8000-000000000903"
		recordID     = "019551d0-e9c0-7000-8000-000000000904"
		eventID      = "019551d0-e9c0-7000-8000-000000000905"
		userID       = "019551d0-e9c0-7000-8000-000000000906"
		sessionID    = "019551d0-e9c0-7000-8000-000000000907"
	)
	_, err = pool.Exec(ctx, `
		INSERT INTO pgbase_meta.admins
			(id, email, password_hash, active, created_at, updated_at)
		VALUES ($1, 'legacy-admin@example.test', 'fixture-admin-password-hash', true,
			'2025-01-02 03:04:05+00', '2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.collections
			(id, name, table_name, status, created_at, updated_at)
		VALUES ($2, 'legacy_notes', 'legacy_notes', 'active',
			'2025-01-02 03:04:05+00', '2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.fields
			(id, collection_id, name, column_name, field_type, required, is_unique,
			 is_indexed, has_default, default_json, ordinal, created_at)
		VALUES ($3, $2, 'title', 'title', 'text', true, false, false, false, NULL, 0,
			'2025-01-02 03:04:05+00');
		CREATE TABLE app.legacy_notes (
			id uuid PRIMARY KEY,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL,
			title text NOT NULL
		);
		INSERT INTO app.legacy_notes (id, created_at, updated_at, title)
		VALUES ($4, '2025-01-02 03:04:05+00', '2025-01-02 03:04:05+00', 'legacy value');
		INSERT INTO pgbase_meta.collection_creation_counters
			(collection_id, generation, last_ordinal, updated_at)
		VALUES ($2, 0, 0, '2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.record_membership_backfills
			(collection_id, generation, state, updated_at)
		VALUES ($2, 0, 'off', '2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.record_audit_events
			(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
			 record_id, operation, schema_version, schema_digest, previous_version,
			 new_version, previous_etag, new_etag, changed_fields, input_hash,
			 idempotency_key_hash, compatibility, committed_at)
		VALUES ($5, 'administrator', $1, 'legacy-request-0001', $2, 'legacy_notes',
			$4, 'create', 1, decode(repeat('11', 32), 'hex'), NULL, 1, NULL,
			'"legacy-etag"', ARRAY['title'], decode(repeat('22', 32), 'hex'),
			decode(repeat('33', 32), 'hex'), 'none', '2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.record_revisions
			(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ($2, $4, 1, decode(repeat('44', 32), 'hex'), $5,
			'2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.record_outbox
			(event_id, topic, collection_id, collection_name, record_id, operation,
			 record_version, envelope, created_at)
		VALUES ($5, 'record.mutation.v1', $2, 'legacy_notes', $4, 'create', 1,
			jsonb_build_object('version', '1', 'eventId', $5::text,
				'collectionId', $2::text, 'collectionName', 'legacy_notes',
				'recordId', $4::text, 'operation', 'create', 'recordVersion', '1'),
			'2025-01-02 03:04:05+00');
		INSERT INTO pgbase_meta.record_idempotency
			(actor_kind, actor_id, collection_id, key_hash, request_hash, record_id,
			 event_id, response_body, etag, created_at, expires_at)
		VALUES ('administrator', $1, $2, decode(repeat('33', 32), 'hex'),
			decode(repeat('22', 32), 'hex'), $4, $5,
			jsonb_build_object('id', $4::text, 'title', 'legacy value'), '"legacy-etag"',
			'2025-01-02 03:04:05+00', '2035-01-02 03:04:05+00')`,
		pgx.QueryExecModeSimpleProtocol, adminID, collectionID, fieldID, recordID, eventID)
	if err != nil {
		t.Fatalf("seed populated v9 database: %v", err)
	}

	runner := New(pool, logger)
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("upgrade v9 database through v11: %v", err)
	}

	var legacyTitle string
	var legacyVersion int64
	var auditActorKind string
	var legacyOwnerCount, accessCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT title FROM app.legacy_notes WHERE id = $1),
		(SELECT version FROM pgbase_meta.record_revisions
		 WHERE collection_id = $2 AND record_id = $1),
		(SELECT actor_kind FROM pgbase_meta.record_audit_events WHERE event_id = $3),
		(SELECT count(*) FROM pgbase_meta.record_owners
		 WHERE collection_id = $2 AND record_id = $1),
		(SELECT count(*) FROM pgbase_meta.collection_access WHERE collection_id = $2)`,
		recordID, collectionID, eventID).Scan(
		&legacyTitle, &legacyVersion, &auditActorKind, &legacyOwnerCount, &accessCount,
	); err != nil {
		t.Fatalf("inspect upgraded legacy data: %v", err)
	}
	if legacyTitle != "legacy value" || legacyVersion != 1 || auditActorKind != "administrator" {
		t.Fatalf("upgraded legacy data title=%q version=%d actor_kind=%q", legacyTitle, legacyVersion, auditActorKind)
	}
	if legacyOwnerCount != 0 || accessCount != 0 {
		t.Fatalf("upgrade invented legacy policy rows: owners=%d access=%d", legacyOwnerCount, accessCount)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO pgbase_meta.users
			(id, email, password_hash, active, created_at, updated_at)
		VALUES ($1, 'reader@example.test', 'fixture-user-password-hash', true,
			'2025-02-03 04:05:06+00', '2025-02-03 04:05:06+00');
		INSERT INTO pgbase_meta.user_sessions
			(id, user_id, token_hash, expires_at, created_at)
		VALUES ($2, $1, decode(repeat('55', 32), 'hex'),
			'2035-02-03 04:05:06+00', '2025-02-03 04:05:06+00');
		INSERT INTO pgbase_meta.collection_access (collection_id, enabled)
		VALUES ($3, true);
		INSERT INTO pgbase_meta.record_owners (collection_id, record_id, user_id)
		VALUES ($3, $4, $1)`, pgx.QueryExecModeSimpleProtocol, userID, sessionID, collectionID, recordID)
	if err != nil {
		t.Fatalf("seed v11 identity and ownership data: %v", err)
	}

	var migration10AppliedAt, migration11AppliedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT applied_at FROM pgbase_meta.system_migrations WHERE version = 10),
		(SELECT applied_at FROM pgbase_meta.system_migrations WHERE version = 11)`).Scan(
		&migration10AppliedAt, &migration11AppliedAt,
	); err != nil {
		t.Fatalf("read initial public identity migration receipts: %v", err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("rerun v11 migrations: %v", err)
	}

	var migrationCount, userCount, sessionCount, ownerCount, enabledAccessCount int
	var legacyRecordCount, legacyAuditCount, legacyRevisionCount, legacyIdempotencyCount int
	var rerun10AppliedAt, rerun11AppliedAt time.Time
	var outboxTitle string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM pgbase_meta.system_migrations),
		(SELECT count(*) FROM pgbase_meta.users WHERE id = $1 AND email = 'reader@example.test'),
		(SELECT count(*) FROM pgbase_meta.user_sessions WHERE id = $2 AND user_id = $1
		 AND token_hash = decode(repeat('55', 32), 'hex')),
		(SELECT count(*) FROM pgbase_meta.record_owners
		 WHERE collection_id = $3 AND record_id = $4 AND user_id = $1),
		(SELECT count(*) FROM pgbase_meta.collection_access
		 WHERE collection_id = $3 AND enabled),
		(SELECT count(*) FROM app.legacy_notes WHERE id = $4 AND title = 'legacy value'),
		(SELECT count(*) FROM pgbase_meta.record_audit_events
		 WHERE event_id = $5 AND actor_kind = 'administrator' AND new_version = 1),
		(SELECT count(*) FROM pgbase_meta.record_revisions
		 WHERE collection_id = $3 AND record_id = $4 AND version = 1 AND last_event_id = $5),
		(SELECT count(*) FROM pgbase_meta.record_idempotency
		 WHERE collection_id = $3 AND record_id = $4 AND event_id = $5),
		(SELECT envelope->>'collectionName' FROM pgbase_meta.record_outbox WHERE event_id = $5),
		(SELECT applied_at FROM pgbase_meta.system_migrations WHERE version = 10),
		(SELECT applied_at FROM pgbase_meta.system_migrations WHERE version = 11)`,
		userID, sessionID, collectionID, recordID, eventID).Scan(
		&migrationCount, &userCount, &sessionCount, &ownerCount, &enabledAccessCount,
		&legacyRecordCount, &legacyAuditCount, &legacyRevisionCount, &legacyIdempotencyCount,
		&outboxTitle, &rerun10AppliedAt, &rerun11AppliedAt,
	); err != nil {
		t.Fatalf("inspect rerun data: %v", err)
	}
	if migrationCount != len(migrations) || userCount != 1 || sessionCount != 1 || ownerCount != 1 || enabledAccessCount != 1 {
		t.Fatalf("rerun counts migrations=%d users=%d sessions=%d owners=%d enabled_access=%d",
			migrationCount, userCount, sessionCount, ownerCount, enabledAccessCount)
	}
	if legacyRecordCount != 1 || legacyAuditCount != 1 || legacyRevisionCount != 1 || legacyIdempotencyCount != 1 {
		t.Fatalf("rerun legacy counts records=%d audit=%d revisions=%d idempotency=%d",
			legacyRecordCount, legacyAuditCount, legacyRevisionCount, legacyIdempotencyCount)
	}
	if outboxTitle != "legacy_notes" {
		t.Fatalf("legacy outbox collection name = %q", outboxTitle)
	}
	if !rerun10AppliedAt.Equal(migration10AppliedAt) || !rerun11AppliedAt.Equal(migration11AppliedAt) {
		t.Fatalf("rerun changed migration receipts: v10 %s -> %s, v11 %s -> %s",
			migration10AppliedAt, rerun10AppliedAt, migration11AppliedAt, rerun11AppliedAt)
	}
}
