package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var (
	ErrCollectionNotFound = errors.New("collection not found")
	ErrRecordNotFound     = errors.New("record not found")
)

type CatalogEnsurer interface {
	EnsureCurrent(context.Context, database.DBTX) (schema.Catalog, error)
}

type RecordPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type Service struct {
	pool    *database.Pool
	schemas CatalogEnsurer
	cursors *CursorCodec
	now     func() time.Time
}

func NewService(pool *database.Pool, schemas CatalogEnsurer, cursorSecret []byte) *Service {
	return &Service{pool: pool, schemas: schemas, cursors: NewCursorCodec(cursorSecret), now: time.Now}
}

func (s *Service) List(ctx context.Context, collectionName, cursor string, limit int) (RecordPage, error) {
	if err := s.ready(); err != nil {
		return RecordPage{}, err
	}
	var after *PageCursor
	if cursor != "" {
		decoded, err := s.cursors.Decode(cursor)
		if err != nil {
			return RecordPage{}, err
		}
		after = &decoded
	}
	page := RecordPage{Items: []map[string]any{}}
	err := s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		statement := CompileList(collection, after, limit)
		rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			record, err := EncodeRecord(collection, rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		pageLimit := normalizedFetchLimit(limit) - 1
		if len(page.Items) > pageLimit {
			page.Items = page.Items[:pageLimit]
			createdAt, id, err := recordPagePosition(page.Items[len(page.Items)-1])
			if err != nil {
				return err
			}
			page.NextCursor, err = s.cursors.Encode(PageCursor{Version: cursorVersion, CreatedAt: createdAt, ID: id})
			if err != nil {
				return err
			}
		}
		return nil
	}, collectionName)
	return page, err
}

func (s *Service) Get(ctx context.Context, collectionName string, id uuid.UUID) (map[string]any, error) {
	var record map[string]any
	err := s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		statement := CompileSelectByID(collection, id)
		var err error
		record, err = EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		return recordQueryError(err)
	}, collectionName)
	return record, err
}

func (s *Service) Create(ctx context.Context, collectionName string, input map[string]json.RawMessage) (map[string]any, error) {
	var record map[string]any
	err := s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate record id: %w", err)
		}
		statement, err := CompileInsert(collection, input, id, s.now().UTC())
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
	err := s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		statement, err := CompileUpdate(collection, id, input, s.now().UTC())
		if err != nil {
			return err
		}
		record, err = EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		return recordQueryError(err)
	}, collectionName)
	return record, err
}

func (s *Service) Delete(ctx context.Context, collectionName string, id uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		statement := CompileDelete(collection, id)
		tag, err := tx.Exec(ctx, statement.SQL, statement.Args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrRecordNotFound
		}
		return nil
	}, collectionName)
}

func (s *Service) inTx(ctx context.Context, operation func(pgx.Tx, schema.Collection) error, collectionName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[collectionName]
		if !ok {
			return ErrCollectionNotFound
		}
		return operation(tx, collection)
	})
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.schemas == nil || s.cursors == nil || s.now == nil {
		return errors.New("record service is unavailable")
	}
	return nil
}

func recordPagePosition(record map[string]any) (time.Time, uuid.UUID, error) {
	createdAtValue, createdAtOK := record["created_at"].(string)
	idValue, idOK := record["id"].(string)
	if !createdAtOK || !idOK {
		return time.Time{}, uuid.Nil, errors.New("encode page cursor: invalid record position")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return time.Time{}, uuid.Nil, errors.New("encode page cursor: invalid record timestamp")
	}
	id, err := uuid.Parse(idValue)
	if err != nil {
		return time.Time{}, uuid.Nil, errors.New("encode page cursor: invalid record id")
	}
	return createdAt, id, nil
}

func recordQueryError(err error) error {
	if err == nil {
		return nil
	}
	var scanError *RecordScanError
	if errors.As(err, &scanError) && errors.Is(scanError.diagnosticCause(), pgx.ErrNoRows) {
		return ErrRecordNotFound
	}
	if errors.As(err, &scanError) {
		return scanError.diagnosticCause()
	}
	return err
}
