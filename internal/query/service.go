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

const (
	cursorTTL              = 15 * time.Minute
	maximumRecordPageBytes = 4 << 20
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

func (s *Service) List(ctx context.Context, collectionName, cursorToken string, limit int, filterText, sortText string) (RecordPage, error) {
	if err := s.ready(); err != nil {
		return RecordPage{}, err
	}
	filterNode, err := parseFilterRequest(filterText)
	if err != nil {
		return RecordPage{}, err
	}
	requestedSort, err := parseSort(sortText)
	if err != nil {
		return RecordPage{}, err
	}
	sortKeys := effectiveSortKeys(requestedSort)
	page := RecordPage{Items: []map[string]any{}}
	err = s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
		operationTime := s.now().UTC()
		var after *PageCursor
		if cursorToken != "" {
			decoded, err := s.cursors.Decode(cursorToken, collection.ID, operationTime)
			if err != nil {
				return err
			}
			if !sameSortKeys(decoded.Sort, sortKeys) {
				return invalidCursor()
			}
			after = &decoded
		}
		statement, err := CompileList(collection, ListQuery{Filter: filterNode, Sort: requestedSort, After: after, Limit: limit})
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		pageLimit := normalizedFetchLimit(limit) - 1
		responseBytes := 0
		hasMore := false
		for rows.Next() {
			record, err := EncodeRecord(collection, rows)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("measure record page item: %w", err)
			}
			if len(page.Items) > 0 && responseBytes+len(encoded) > maximumRecordPageBytes {
				hasMore = true
				break
			}
			page.Items = append(page.Items, record)
			responseBytes += len(encoded)
			if len(page.Items) > pageLimit {
				page.Items = page.Items[:pageLimit]
				hasMore = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if hasMore && len(page.Items) > 0 {
			values, err := recordSortValues(sortKeys, page.Items[len(page.Items)-1])
			if err != nil {
				return err
			}
			page.NextCursor, err = s.cursors.Encode(PageCursor{
				Version: cursorVersion, CollectionID: collection.ID,
				Sort: sortKeys, Values: values, ExpiresAt: operationTime.Add(cursorTTL),
			})
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
	err := s.inTx(ctx, func(tx pgx.Tx, collection schema.Collection) error {
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

func recordSortValues(keys []SortKey, record map[string]any) ([]json.RawMessage, error) {
	values := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		value, present := record[key.Field]
		if !present {
			return nil, errors.New("encode page cursor: missing sort value")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode page cursor: %w", err)
		}
		values = append(values, raw)
	}
	return values, nil
}

func sameSortKeys(left, right []SortKey) bool {
	if len(left) != len(right) {
		return false
	}
	for index, key := range left {
		if key.Field != right[index].Field || key.Desc != right[index].Desc {
			return false
		}
	}
	return true
}

// parseFilterRequest turns an empty parameter into no filter and otherwise
// parses the expression into a validation-checked node.
func parseFilterRequest(filterText string) (filterNode, error) {
	if filterText == "" {
		return nil, nil
	}
	return parseFilter(filterText)
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
