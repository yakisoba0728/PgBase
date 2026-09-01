package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
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

type RecordPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type Service struct {
	pool         *database.Pool
	schemas      *schema.Service
	cursors      *CursorCodec
	policy       ProjectionPolicy
	expandLimits expandLimits
	now          func() time.Time
	logger       *slog.Logger
	recorder     observability.Recorder
}

func NewService(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return NewServiceWithRecorder(pool, schemas, cursorSecret, logger, nil)
}

func NewServiceWithRecorder(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return NewServiceWithProjectionPolicy(pool, schemas, cursorSecret, AdminAllowAll{}, logger, recorder)
}

func NewServiceWithProjectionPolicy(
	pool *database.Pool,
	schemas *schema.Service,
	cursorSecret []byte,
	policy ProjectionPolicy,
	logger *slog.Logger,
	recorder observability.Recorder,
) *Service {
	return &Service{
		pool: pool, schemas: schemas, cursors: NewCursorCodec(cursorSecret), now: time.Now,
		policy: policy, expandLimits: defaultExpandLimits,
		logger: logger, recorder: recorder,
	}
}

func (s *Service) List(ctx context.Context, request ListRequest) (RecordPage, error) {
	result, err := s.readList(ctx, request)
	return result.page, err
}

func (s *Service) Get(ctx context.Context, collectionName string, id uuid.UUID) (map[string]any, error) {
	var record map[string]any
	err := s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		statement := CompileSelectByID(collection, id)
		var err error
		record, err = EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err := recordQueryError(err); err != nil {
			return err
		}
		return hydrateRelationRecords(ctx, tx, catalog, collection, []map[string]any{record})
	}, collectionName)
	return record, err
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

// NormalizeRecordError converts the deliberately opaque row-scan sentinel into
// the stable record-not-found error while keeping all other diagnostic causes
// out of public error strings. Transaction kernels that reuse the query codec
// should call this at their database boundary.
func NormalizeRecordError(err error) error { return recordQueryError(err) }
