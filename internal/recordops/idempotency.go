package recordops

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
)

type outboxEnvelope struct {
	Version        int            `json:"version"`
	EventID        uuid.UUID      `json:"eventId"`
	CollectionID   uuid.UUID      `json:"collectionId"`
	CollectionName string         `json:"collectionName"`
	RecordID       uuid.UUID      `json:"recordId"`
	Operation      string         `json:"operation"`
	RecordVersion  int64          `json:"recordVersion"`
	Changes        map[string]any `json:"changes,omitempty"`
	Tombstone      bool           `json:"tombstone,omitempty"`
}

func findIdempotentCreate(ctx context.Context, tx pgx.Tx, principal Principal, collectionID uuid.UUID,
	keyHash, requestHash []byte,
) (MutationResult, bool, error) {
	var storedHash []byte
	var recordID, eventID uuid.UUID
	var body []byte
	var etag string
	var expiresAt time.Time
	err := tx.QueryRow(ctx, `SELECT request_hash, record_id, event_id, response_body, etag, expires_at
		FROM pgbase_meta.record_idempotency
		WHERE actor_kind = $1 AND actor_id = $2 AND collection_id = $3 AND key_hash = $4`,
		string(principal.Kind), principal.ID, collectionID, keyHash).
		Scan(&storedHash, &recordID, &eventID, &body, &etag, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, false, nil
	}
	if err != nil {
		return MutationResult{}, false, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&now); err != nil {
		return MutationResult{}, false, err
	}
	if !expiresAt.After(now) {
		if _, err := tx.Exec(ctx, `DELETE FROM pgbase_meta.record_idempotency
			WHERE actor_kind = $1 AND actor_id = $2 AND collection_id = $3 AND key_hash = $4`,
			string(principal.Kind), principal.ID, collectionID, keyHash); err != nil {
			return MutationResult{}, false, err
		}
		return MutationResult{}, false, nil
	}
	if !bytes.Equal(storedHash, requestHash) {
		return MutationResult{}, false, &Error{Code: CodeIdempotencyConflict}
	}
	data, err := decodeStoredRecord(body)
	if err != nil {
		return MutationResult{}, false, errors.New("decode stored idempotency result")
	}
	return MutationResult{
		Record:  &Record{Data: data, ETag: etag, Version: 1},
		EventID: eventID, Replayed: true,
	}, true, nil
}

func writeIdempotentCreate(ctx context.Context, tx pgx.Tx, principal Principal, collectionID uuid.UUID,
	keyHash, requestHash []byte, recordID, eventID uuid.UUID, body []byte, etag string,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_idempotency
		(actor_kind, actor_id, collection_id, key_hash, request_hash, record_id,
		 event_id, response_body, etag, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9,
			transaction_timestamp(), transaction_timestamp() + $10::interval)`,
		string(principal.Kind), principal.ID, collectionID, keyHash, requestHash,
		recordID, eventID, string(body), etag, "24 hours")
	return err
}

func advisoryLockKey(principal Principal, collectionScope, keyHash []byte) int64 {
	digest := recordartifact.Digest("pgbase-idempotency-lock-v1", []byte(principal.Kind), principal.ID[:], collectionScope, keyHash)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
