package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// errRevisionChanged keeps the single-record contract: a revision the kernel
// did not observe cannot be advanced or removed, and the mutation aborts
// rather than committing an artifact set that disagrees with the row.
var errRevisionChanged = errors.New("record revision changed outside the transaction kernel")

var errArtifactCountMismatch = errors.New("record artifact batch wrote an unexpected row count")

type artifactRow struct {
	eventID            uuid.UUID
	recordID           uuid.UUID
	previousVersion    *int64
	newVersion         int64
	previousETag       string
	newETag            string
	changedFields      []string
	changes            map[string]any
	tombstone          bool
	idempotencyKeyHash []byte
	compatibility      Compatibility
	validator          []byte
	revisionPresent    bool
}

// artifactBatch is every row one collection contributes to one operation of a
// mutation. Row order is load-bearing: record_audit_events.sequence is an
// identity column assigned in insert order, and consumers replay a request's
// events by it.
type artifactBatch struct {
	collection    schema.Collection
	operation     string
	schemaVersion int64
	schemaDigest  [32]byte
	rows          []artifactRow
}

// commitMutation writes the revision, audit and outbox artifacts of one
// mutation. Every statement is set-based per collection and operation and
// verifies its own affected-row count, so a cascade closure of up to 10,000
// records costs a fixed number of statements per collection instead of four
// per record.
func commitMutation(
	ctx context.Context,
	tx pgx.Tx,
	principal Principal,
	requestID string,
	inputHash []byte,
	batches ...artifactBatch,
) error {
	for _, batch := range batches {
		if len(batch.rows) == 0 {
			continue
		}
		if err := writeAuditBatch(ctx, tx, principal, requestID, inputHash, batch); err != nil {
			return err
		}
		if err := writeRevisionBatch(ctx, tx, batch); err != nil {
			return err
		}
		if err := writeOutboxBatch(ctx, tx, batch); err != nil {
			return err
		}
	}
	return nil
}

func writeAuditBatch(
	ctx context.Context,
	tx pgx.Tx,
	principal Principal,
	requestID string,
	inputHash []byte,
	batch artifactBatch,
) error {
	count := len(batch.rows)
	eventIDs := make([]uuid.UUID, count)
	recordIDs := make([]uuid.UUID, count)
	previousVersions := make([]*int64, count)
	newVersions := make([]int64, count)
	previousETags := make([]*string, count)
	newETags := make([]*string, count)
	changedFields := make([]string, count)
	idempotencyHashes := make([][]byte, count)
	compatibilities := make([]string, count)
	for index, row := range batch.rows {
		encoded, err := json.Marshal(nonNilFields(row.changedFields))
		if err != nil {
			return fmt.Errorf("encode changed record fields: %w", err)
		}
		eventIDs[index] = row.eventID
		recordIDs[index] = row.recordID
		previousVersions[index] = row.previousVersion
		newVersions[index] = row.newVersion
		previousETags[index] = optionalText(row.previousETag)
		newETags[index] = optionalText(row.newETag)
		changedFields[index] = string(encoded)
		idempotencyHashes[index] = row.idempotencyKeyHash
		compatibilities[index] = "none"
		if row.compatibility != CompatibilityNone {
			compatibilities[index] = string(row.compatibility)
		}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
		 record_id, operation, schema_version, schema_digest, previous_version,
		 new_version, previous_etag, new_etag, changed_fields, input_hash,
		 idempotency_key_hash, compatibility, committed_at)
		SELECT event_id, $1, $2, $3, $4, $5, record_id, $6, $7, $8, previous_version,
			new_version, previous_etag, new_etag,
			array(select jsonb_array_elements_text(changed_fields::jsonb)), $9,
			idempotency_key_hash, compatibility, transaction_timestamp()
		FROM unnest($10::uuid[], $11::uuid[], $12::bigint[], $13::bigint[], $14::text[],
			$15::text[], $16::text[], $17::bytea[], $18::text[])
			AS rows(event_id, record_id, previous_version, new_version, previous_etag,
				new_etag, changed_fields, idempotency_key_hash, compatibility)`,
		string(principal.Kind), principal.ID, requestID, batch.collection.ID, batch.collection.Name,
		batch.operation, batch.schemaVersion, batch.schemaDigest[:], inputHash,
		eventIDs, recordIDs, previousVersions, newVersions, previousETags,
		newETags, changedFields, idempotencyHashes, compatibilities)
	if err != nil {
		return err
	}
	return expectRows(tag, count, errArtifactCountMismatch)
}

// writeRevisionBatch keeps the per-record optimistic guard of the single-row
// writer: every mutation advances a record exactly one version, so the stored
// version the upsert requires is EXCLUDED.version - 1. A row whose revision
// changed outside this transaction is simply not written and the affected-row
// count reports it.
func writeRevisionBatch(ctx context.Context, tx pgx.Tx, batch artifactBatch) error {
	if batch.operation == "delete" {
		return deleteRevisionBatch(ctx, tx, batch)
	}
	count := len(batch.rows)
	recordIDs := make([]uuid.UUID, count)
	newVersions := make([]int64, count)
	validators := make([][]byte, count)
	eventIDs := make([]uuid.UUID, count)
	for index, row := range batch.rows {
		if len(row.validator) != 32 {
			return errors.New("invalid record revision validator")
		}
		recordIDs[index] = row.recordID
		newVersions[index] = row.newVersion
		validators[index] = row.validator
		eventIDs[index] = row.eventID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		SELECT $1, record_id, version, validator, last_event_id, transaction_timestamp()
		FROM unnest($2::uuid[], $3::bigint[], $4::bytea[], $5::uuid[])
			AS rows(record_id, version, validator, last_event_id)
		ON CONFLICT (collection_id, record_id) DO UPDATE SET
			version = EXCLUDED.version,
			validator = EXCLUDED.validator,
			last_event_id = EXCLUDED.last_event_id,
			updated_at = EXCLUDED.updated_at
		WHERE pgbase_meta.record_revisions.version = EXCLUDED.version - 1`,
		batch.collection.ID, recordIDs, newVersions, validators, eventIDs)
	if err != nil {
		return err
	}
	return expectRows(tag, count, errRevisionChanged)
}

func deleteRevisionBatch(ctx context.Context, tx pgx.Tx, batch artifactBatch) error {
	recordIDs := make([]uuid.UUID, 0, len(batch.rows))
	versions := make([]int64, 0, len(batch.rows))
	for _, row := range batch.rows {
		if !row.revisionPresent {
			continue
		}
		recordIDs = append(recordIDs, row.recordID)
		versions = append(versions, *row.previousVersion)
	}
	if len(recordIDs) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, `DELETE FROM pgbase_meta.record_revisions
		USING unnest($2::uuid[], $3::bigint[]) AS rows(record_id, version)
		WHERE pgbase_meta.record_revisions.collection_id = $1
			AND pgbase_meta.record_revisions.record_id = rows.record_id
			AND pgbase_meta.record_revisions.version = rows.version`,
		batch.collection.ID, recordIDs, versions)
	if err != nil {
		return err
	}
	return expectRows(tag, len(recordIDs), errRevisionChanged)
}

func writeOutboxBatch(ctx context.Context, tx pgx.Tx, batch artifactBatch) error {
	count := len(batch.rows)
	eventIDs := make([]uuid.UUID, count)
	recordIDs := make([]uuid.UUID, count)
	versions := make([]int64, count)
	envelopes := make([]string, count)
	for index, row := range batch.rows {
		envelope, err := json.Marshal(outboxEnvelope{
			Version: outboxEnvelopeVersion, EventID: row.eventID, CollectionID: batch.collection.ID,
			CollectionName: batch.collection.Name, RecordID: row.recordID, Operation: batch.operation,
			RecordVersion: row.newVersion, Changes: mutationChanges(row.changes, row.changedFields),
			Tombstone: row.tombstone,
		})
		if err != nil {
			return fmt.Errorf("encode record outbox event: %w", err)
		}
		eventIDs[index] = row.eventID
		recordIDs[index] = row.recordID
		versions[index] = row.newVersion
		envelopes[index] = string(envelope)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox
		(event_id, topic, collection_id, collection_name, record_id, operation,
		 record_version, envelope, created_at)
		SELECT event_id, 'record.mutation.v1', $1, $2, record_id, $3, record_version,
			envelope::jsonb, transaction_timestamp()
		FROM unnest($4::uuid[], $5::uuid[], $6::bigint[], $7::text[])
			AS rows(event_id, record_id, record_version, envelope)`,
		batch.collection.ID, batch.collection.Name, batch.operation,
		eventIDs, recordIDs, versions, envelopes)
	if err != nil {
		return err
	}
	return expectRows(tag, count, errArtifactCountMismatch)
}

// readRevisionBatch reads one collection's stored revisions in a single query.
// A record with no revision row is legacy data an older build wrote and is
// reported by its absence, exactly like the single-record reader.
func readRevisionBatch(
	ctx context.Context,
	tx pgx.Tx,
	collectionID uuid.UUID,
	recordIDs []uuid.UUID,
) (map[uuid.UUID]storedRevision, error) {
	revisions := make(map[uuid.UUID]storedRevision, len(recordIDs))
	if len(recordIDs) == 0 {
		return revisions, nil
	}
	rows, err := tx.Query(ctx, `SELECT record_id, version, validator FROM pgbase_meta.record_revisions
		WHERE collection_id = $1 AND record_id = ANY($2::uuid[])`, collectionID, recordIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var recordID uuid.UUID
		var revision storedRevision
		if err := rows.Scan(&recordID, &revision.version, &revision.validator); err != nil {
			return nil, err
		}
		if revision.version < 1 || len(revision.validator) != 32 {
			return nil, errors.New("invalid record revision state")
		}
		revisions[recordID] = revision
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

type storedRevision struct {
	version   int64
	validator []byte
}

// readRecordBatch reads one collection's base rows in a single query and
// hydrates every multiple relation with one further query per relation field,
// so the whole read is bounded by the collection's field count rather than by
// the number of records.
func readRecordBatch(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	ids []uuid.UUID,
) (map[uuid.UUID]map[string]any, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]map[string]any{}, nil
	}
	decoded, err := relation.ReadRecords(ctx, tx, collection, ids, func(row pgx.Row) (map[string]any, error) {
		return query.EncodeRecord(collection, row)
	})
	if err != nil {
		return nil, normalizeRelationError(query.NormalizeRecordError(err))
	}
	if len(decoded) != len(ids) {
		return nil, schema.ErrConflict
	}
	records := make(map[uuid.UUID]map[string]any, len(decoded))
	for _, record := range decoded {
		encoded, ok := record["id"].(string)
		if !ok {
			return nil, schema.ErrConflict
		}
		id, err := uuid.Parse(encoded)
		if err != nil {
			return nil, schema.ErrConflict
		}
		records[id] = record
	}
	if len(records) != len(ids) {
		return nil, schema.ErrConflict
	}
	bindings, err := bindRecordRelations(catalog, collection)
	if err != nil {
		return nil, err
	}
	if bindings.HasMultiple() {
		if err := bindings.Hydrate(ctx, tx, ids, records); err != nil {
			return nil, normalizeRelationError(err)
		}
	}
	return records, nil
}

func expectRows(tag pgconn.CommandTag, want int, mismatch error) error {
	if tag.RowsAffected() != int64(want) {
		return mismatch
	}
	return nil
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func nonNilFields(fields []string) []string {
	if fields == nil {
		return []string{}
	}
	return fields
}
