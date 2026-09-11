package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

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

// PocketBasePage is the exact list envelope exposed by the explicit
// PocketBase compatibility adapter.
type PocketBasePage struct {
	Page       int              `json:"page"`
	PerPage    int              `json:"perPage"`
	TotalItems int64            `json:"totalItems"`
	TotalPages int64            `json:"totalPages"`
	Items      []map[string]any `json:"items"`
}

type PocketBaseListRequest struct {
	ListRequest
	Page      int
	PerPage   int
	SkipTotal bool
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

// ListPocketBase reads one offset page and its optional total in a single
// repeatable-read snapshot. It is deliberately separate from List so native
// callers keep the cursor wire contract.
func ListPocketBase(ctx context.Context, s *Service, request PocketBaseListRequest) (page PocketBasePage, err error) {
	if err := s.ready(); err != nil {
		return page, err
	}
	if !request.Principal.Valid() {
		return page, validation("principal", "invalid")
	}
	filterNode, err := parsePocketBaseFilterRequest(request.Filter)
	if err != nil {
		return page, err
	}
	filterNode = markPocketBaseLike(filterNode)
	sortKeys, err := parseSort(request.Sort)
	if err != nil {
		return page, err
	}
	request.Page, request.PerPage = normalizePocketBasePage(request.Page, request.PerPage)
	if request.Page-1 > int(^uint(0)>>1)/request.PerPage {
		return page, validation("page", "invalid_page")
	}
	request.ListRequest.Limit = request.PerPage
	request.ListRequest.Offset = (request.Page - 1) * request.PerPage
	request.ListRequest.Cursor = ""
	request.ListRequest.PocketBaseCompatibility = true
	page = PocketBasePage{Page: request.Page, PerPage: request.PerPage, TotalItems: -1, TotalPages: -1, Items: []map[string]any{}}
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if !request.SkipTotal {
			total, countErr := s.countListInTx(ctx, tx, catalog, collection, request.ListRequest, filterNode)
			if countErr != nil {
				return countErr
			}
			page.TotalItems = total
			page.TotalPages = (total + int64(request.PerPage) - 1) / int64(request.PerPage)
			if s.countPageHook != nil {
				s.countPageHook()
			}
		}
		result, _, readErr := s.readListInTx(ctx, tx, catalog, collection, request.ListRequest, filterNode, sortKeys)
		page.Items = result.page.Items
		return readErr
	}, request.Collection)
	return page, err
}

func normalizePocketBasePage(page, perPage int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 30
	} else if perPage > 1000 {
		perPage = 1000
	}
	return page, perPage
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
		filterNode, err = parsePocketBaseFilterRequest(request.Filter)
		if err != nil {
			return 0, err
		}
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

func parsePocketBaseFilterRequest(filterText string) (filterNode, error) {
	normalized, err := normalizePocketBaseFilterQuotes(filterText)
	if err != nil {
		return nil, validation("filter", "invalid_filter")
	}
	return parseFilterRequest(normalized)
}

// PocketBase SDK filter bindings emit JSON quoted string literals. Convert
// complete quoted tokens while keeping embedded operators inside the literal.
func normalizePocketBaseFilterQuotes(input string) (string, error) {
	var output strings.Builder
	for position := 0; position < len(input); {
		if input[position] != '"' {
			output.WriteByte(input[position])
			position++
			continue
		}
		start, escaped := position, false
		position++
		for position < len(input) {
			current := input[position]
			position++
			if current == '"' && !escaped {
				break
			}
			if current == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
		}
		if position > len(input) || input[position-1] != '"' {
			return "", errors.New("unterminated PocketBase filter string")
		}
		value, err := strconv.Unquote(input[start:position])
		if err != nil {
			return "", err
		}
		output.WriteByte('\'')
		output.WriteString(strings.ReplaceAll(value, "'", "''"))
		output.WriteByte('\'')
	}
	return output.String(), nil
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
