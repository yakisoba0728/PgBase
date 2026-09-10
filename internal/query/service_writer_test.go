package query

// These helpers intentionally exist only in the test binary. Production
// record writes must pass through internal/recordops so revision, audit,
// outbox, idempotency, principal, and precondition invariants cannot be
// bypassed through query.Service.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) Create(ctx context.Context, collectionName string, input map[string]json.RawMessage) (map[string]any, error) {
	var record map[string]any
	err := s.inTxCatalog(ctx, func(tx pgx.Tx, _ schema.Catalog, collection schema.Collection) error {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate record id: %w", err)
		}
		statement, err := CompileInsert(collection, input, id)
		if err != nil {
			return err
		}
		record, err = EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err := recordQueryError(err); err != nil {
			return err
		}
		return assignCreationOrdinal(ctx, tx, collection.ID, id)
	}, collectionName)
	return record, err
}

// The reader admits only a membership boundary that has retired cursor v4, so
// these writes have to reserve the same sidecar ordinal recordops reserves.
// A base row without one is deliberately invisible to a list, which would look
// like a reader bug rather than a missing write.
func assignCreationOrdinal(ctx context.Context, tx pgx.Tx, collectionID, recordID uuid.UUID) error {
	var ordinal int64
	if err := tx.QueryRow(ctx, `update pgbase_meta.collection_creation_counters
		set last_ordinal = last_ordinal + 1, updated_at = transaction_timestamp()
		where collection_id = $1 returning last_ordinal`, collectionID).Scan(&ordinal); err != nil {
		return fmt.Errorf("reserve record creation ordinal: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, $3)`,
		collectionID, recordID, ordinal); err != nil {
		return fmt.Errorf("insert record creation ordinal: %w", err)
	}
	return nil
}

func (s *Service) Update(ctx context.Context, collectionName string, id uuid.UUID, input map[string]json.RawMessage) (map[string]any, error) {
	var record map[string]any
	err := s.inTxCatalog(ctx, func(tx pgx.Tx, _ schema.Catalog, collection schema.Collection) error {
		statement, err := CompileUpdate(collection, id, input)
		if err != nil {
			return err
		}
		record, err = EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		return recordQueryError(err)
	}, collectionName)
	return record, err
}

func (s *Service) Delete(ctx context.Context, collectionName string, id uuid.UUID) error {
	return s.inTxCatalog(ctx, func(tx pgx.Tx, _ schema.Catalog, collection schema.Collection) error {
		tag, err := tx.Exec(ctx, "DELETE FROM "+quotedTable(collection)+" WHERE "+quoted("id")+" = $1", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrRecordNotFound
		}
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.record_creation_ordinals
			where collection_id = $1 and record_id = $2`, collection.ID, id); err != nil {
			return fmt.Errorf("delete record creation ordinal: %w", err)
		}
		return nil
	}, collectionName)
}
