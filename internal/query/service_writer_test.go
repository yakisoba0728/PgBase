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
	"github.com/webisnoteasy/PgBase/internal/schema"
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
		return recordQueryError(err)
	}, collectionName)
	return record, err
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
		return nil
	}, collectionName)
}
