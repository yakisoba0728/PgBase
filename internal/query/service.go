package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	maximumTxRetries       = 32
	maximumTxRetryDelay    = 100 * time.Millisecond
)

type CatalogEnsurer interface {
	EnsureCurrent(context.Context, database.DBTX) (schema.Catalog, error)
}

type RecordPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type Service struct {
	pool         *database.Pool
	schemas      CatalogEnsurer
	cursors      *CursorCodec
	policy       ProjectionPolicy
	expandLimits expandLimits
	now          func() time.Time
	logger       *slog.Logger
	recorder     observability.Recorder
}

func NewService(pool *database.Pool, schemas CatalogEnsurer, cursorSecret []byte, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return NewServiceWithRecorder(pool, schemas, cursorSecret, logger, nil)
}

func NewServiceWithRecorder(pool *database.Pool, schemas CatalogEnsurer, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return NewServiceWithProjectionPolicy(pool, schemas, cursorSecret, AdminAllowAll{}, logger, recorder)
}

func NewServiceWithProjectionPolicy(
	pool *database.Pool,
	schemas CatalogEnsurer,
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
	page := RecordPage{Items: []map[string]any{}}
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		root, err := s.readRootPageInTx(ctx, tx, collection, rootPageReadRequest{
			cursorToken: cursorToken, limit: limit, filter: filterNode, requestedSort: requestedSort,
			operationTime: s.now().UTC(),
			hydrate: func(items []map[string]any) error {
				return hydrateRelationRecords(ctx, tx, catalog, collection, items)
			},
		})
		if err != nil {
			return err
		}
		page = root.page
		return nil
	}, collectionName)
	return page, err
}

// finalizeRecordPage measures the exact JSON object emitted by the API,
// including commas, the envelope, the continuation cursor, and writeJSON's
// trailing newline. Item-only accounting can otherwise cross the advertised
// bound as soon as a cursor is added. A single item that cannot fit is rejected
// with a stable error instead of being silently skipped by cursor advancement.
func (s *Service) finalizeRecordPage(page *RecordPage, collectionID uuid.UUID, sortKeys []SortKey, expiresAt time.Time, hasMore bool) error {
	// Item encodings never change while the tail is trimmed, so they are
	// measured once. Only the envelope is re-encoded per attempt, because its
	// size follows the continuation cursor, which follows the anchor record.
	// Re-marshalling the whole page on every attempt was quadratic in the page
	// size for a bound that is exceeded by a few kilobytes.
	itemBytes := make([]int, len(page.Items))
	measured := 0
	for index, item := range page.Items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("measure record page response: %w", err)
		}
		itemBytes[index] = len(encoded)
		measured += len(encoded)
	}
	for {
		page.NextCursor = ""
		if hasMore && len(page.Items) > 0 {
			anchor := page.Items[len(page.Items)-1]
			values, err := recordSortValues(sortKeys, anchor)
			if err != nil {
				return err
			}
			anchorText, ok := anchor[systemFieldID].(string)
			if !ok {
				return errors.New("encode page cursor: invalid record id")
			}
			anchorID, err := uuid.Parse(anchorText)
			if err != nil || anchorID == uuid.Nil || anchorID.String() != anchorText {
				return errors.New("encode page cursor: invalid record id")
			}
			page.NextCursor, err = s.cursors.Encode(PageCursor{
				Version: cursorVersion, CollectionID: collectionID,
				Sort: sortKeys, Values: values, AnchorID: anchorID, ExpiresAt: expiresAt,
			})
			if err != nil {
				return err
			}
		}

		// An empty page encodes the exact envelope this page will emit; the
		// items add their own bytes plus one separating comma each.
		envelope, err := json.Marshal(RecordPage{Items: []map[string]any{}, NextCursor: page.NextCursor})
		if err != nil {
			return fmt.Errorf("measure record page response: %w", err)
		}
		responseBytes := len(envelope) + measured
		if len(page.Items) > 0 {
			responseBytes += len(page.Items) - 1
		}
		if responseBytes+1 <= maximumRecordPageBytes {
			return nil
		}
		if len(page.Items) <= 1 {
			return validation("response", "response_too_large")
		}
		measured -= itemBytes[len(page.Items)-1]
		page.Items = page.Items[:len(page.Items)-1]
		hasMore = true
	}
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

func (s *Service) inTx(ctx context.Context, operation func(pgx.Tx, schema.Collection) error, collectionName string) error {
	return s.inTxCatalog(ctx, func(tx pgx.Tx, _ schema.Catalog, collection schema.Collection) error {
		return operation(tx, collection)
	}, collectionName)
}

func (s *Service) inTxCatalog(ctx context.Context, operation func(pgx.Tx, schema.Catalog, schema.Collection) error, collectionName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
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
		cause, retryable := transactionRetryCause(err)
		if err == nil || !retryable {
			return err
		}
		if attempt >= maximumTxRetries {
			s.record(ctx, observability.TransactionRetryEvent(cause, true))
			if s.logger != nil {
				s.logger.WarnContext(ctx, "record transaction retries exhausted", "cause", cause, "attempts", attempt+1)
			}
			return err
		}

		delay := transactionRetryDelay(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			s.record(ctx, observability.TransactionRetryEvent(cause, false))
			if s.logger != nil {
				s.logger.WarnContext(ctx, "retrying record transaction", "cause", cause, "attempt", attempt+1, "retry_in", delay)
			}
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
}

func transactionRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Millisecond
	for step := 0; step < attempt && delay < maximumTxRetryDelay; step++ {
		delay *= 2
		if delay > maximumTxRetryDelay {
			delay = maximumTxRetryDelay
		}
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

func transactionRetryCause(err error) (observability.RetryCause, bool) {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return "", false
	}
	switch postgresError.Code {
	case "0A000":
		return observability.RetryCauseCachedPlan, true
	case "40001":
		return observability.RetryCauseSerialization, true
	case "40P01":
		return observability.RetryCauseDeadlock, true
	default:
		return "", false
	}
}

func retryableTransactionError(err error) bool {
	_, retryable := transactionRetryCause(err)
	return retryable
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
