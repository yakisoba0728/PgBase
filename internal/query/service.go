package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var (
	ErrCollectionNotFound = errors.New("collection not found")
	ErrRecordNotFound     = errors.New("record not found")
)

const (
	cursorTTL              = membership.CursorTTL
	maximumRecordPageBytes = 48<<20 + 4<<10
)

type RecordPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type Record = recordartifact.Record

type Service struct {
	pool             *database.Pool
	schemas          *schema.Service
	cursors          *cursor.Codec
	expandLimits     expandLimits
	resolveTraversal func(context.Context, pgx.Tx, uuid.UUID) (membership.Traversal, error)
	logger           *slog.Logger
	recorder         observability.Recorder
	countPageHook    func()
}

func NewService(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return &Service{
		pool: pool, schemas: schemas, cursors: cursor.NewCodec(cursorSecret),
		expandLimits: defaultExpandLimits, resolveTraversal: membership.ResolveTraversal, logger: logger, recorder: recorder,
	}
}

func (s *Service) List(ctx context.Context, request ListRequest) (RecordPage, error) {
	result, err := s.readList(ctx, request)
	return result.page, err
}

// Count evaluates the same filter and access plan as List inside one
// repeatable-read transaction. It requests only record identities and never
// exposes a page boundary as the result.
func Count(ctx context.Context, s *Service, request ListRequest) (total int64, err error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if !request.Principal.Valid() {
		return 0, validation("principal", "invalid")
	}
	filterNode, err := parseFilterRequest(request.Filter)
	if err != nil {
		return 0, err
	}
	if request.PocketBaseCompatibility {
		filterNode = markPocketBaseLike(filterNode)
	}
	sortKeys, err := parseSort(request.Sort)
	if err != nil {
		return 0, err
	}
	request.Limit, request.Fields, request.Expand = 200, "id", ""
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		request.Cursor = ""
		pageNumber := 0
		for {
			result, _, readErr := s.readListInTx(ctx, tx, catalog, collection, request, filterNode, sortKeys)
			if readErr != nil {
				return readErr
			}
			total += int64(len(result.page.Items))
			pageNumber++
			if pageNumber == 1 && s.countPageHook != nil {
				s.countPageHook()
			}
			if result.page.NextCursor == "" {
				return nil
			}
			request.Cursor = result.page.NextCursor
		}
	}, request.Collection)
	return total, err
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
	if s == nil || s.pool == nil || s.schemas == nil || s.cursors == nil || s.resolveTraversal == nil {
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
