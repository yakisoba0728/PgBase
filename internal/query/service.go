package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var (
	ErrCollectionNotFound = errors.New("collection not found")
	ErrRecordNotFound     = errors.New("record not found")
)

const (
	cursorTTL              = membership.CursorTTL
	maximumRecordPageBytes = 4 << 20
)

type RecordPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type Record = recordartifact.Record

type Service struct {
	pool               *database.Pool
	schemas            *schema.Service
	cursors            *cursor.Codec
	expandLimits       expandLimits
	transactionTimeSQL string
	logger             *slog.Logger
	recorder           observability.Recorder
}

func NewService(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return NewServiceWithRecorder(pool, schemas, cursorSecret, logger, nil)
}

func NewServiceWithRecorder(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return &Service{
		pool: pool, schemas: schemas, cursors: cursor.NewCodec(cursorSecret),
		expandLimits: defaultExpandLimits, transactionTimeSQL: `select transaction_timestamp()`, logger: logger, recorder: recorder,
	}
}

func (s *Service) List(ctx context.Context, request ListRequest) (RecordPage, error) {
	result, err := s.readList(ctx, request)
	return result.page, err
}

func (s *Service) inTxCatalog(ctx context.Context, operation func(pgx.Tx, schema.Catalog, schema.Collection) error, collectionName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			catalog, err := s.schemas.EnsureCurrent(ctx, tx)
			if err != nil {
				return err
			}
			collection, ok := catalog.Collections[collectionName]
			if !ok {
				return ErrCollectionNotFound
			}
			return operation(tx, catalog, collection)
		})
	}, func(cause database.RetryCause, exhausted bool) {
		s.record(ctx, observability.TransactionRetryEvent(cause, exhausted))
		if s.logger == nil {
			return
		}
		message := "retrying record transaction"
		if exhausted {
			message = "record transaction retries exhausted"
		}
		s.logger.WarnContext(ctx, message, "cause", cause)
	})
}

func (s *Service) record(ctx context.Context, event observability.Event) {
	if s != nil && s.recorder != nil {
		s.recorder.Record(ctx, event)
	}
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.schemas == nil || s.cursors == nil || s.transactionTimeSQL == "" {
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

// NormalizeRecordError converts the deliberately opaque row-scan sentinel into
// the stable record-not-found error while keeping all other diagnostic causes
// out of public error strings. Transaction kernels that reuse the query codec
// should call this at their database boundary.
func NormalizeRecordError(err error) error { return recordQueryError(err) }
