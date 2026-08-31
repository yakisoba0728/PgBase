package query

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type rootPageReadRequest struct {
	cursorToken   string
	limit         int
	filter        filterNode
	requestedSort []SortKey
	operationTime time.Time

	charge  func(int) error
	hydrate func([]map[string]any) error
}

type rootPageReadResult struct {
	page               RecordPage
	sortKeys           []SortKey
	expiresAt          time.Time
	privateCursor      bool
	cursorValuesByRoot map[string][]json.RawMessage
}

// readRootPageInTx is the single cursor/security/size kernel shared by List
// and ListExpanded. Expansion is deliberately applied only after this returns,
// so target data cannot affect the root anchor, sort hash, or page boundary.
func (s *Service) readRootPageInTx(
	ctx context.Context,
	tx pgx.Tx,
	collection schema.Collection,
	request rootPageReadRequest,
) (rootPageReadResult, error) {
	result := rootPageReadResult{
		page:      RecordPage{Items: []map[string]any{}},
		sortKeys:  effectiveSortKeys(request.requestedSort),
		expiresAt: request.operationTime.Add(cursorTTL),
	}
	charge := request.charge
	if charge == nil {
		charge = func(int) error { return nil }
	}

	var after *PageCursor
	if request.cursorToken != "" {
		decoded, err := s.cursors.Decode(request.cursorToken, collection.ID, request.operationTime)
		if err != nil {
			return rootPageReadResult{}, err
		}
		if !sameSortKeys(decoded.Sort, result.sortKeys) {
			return rootPageReadResult{}, invalidCursor()
		}
		if len(decoded.Values) == 0 {
			if err := charge(1); err != nil {
				return rootPageReadResult{}, err
			}
			statement := CompileSelectByID(collection, decoded.AnchorID)
			anchor, anchorErr := EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
			anchorErr = recordQueryError(anchorErr)
			if errors.Is(anchorErr, ErrRecordNotFound) {
				return rootPageReadResult{}, invalidCursor()
			}
			if anchorErr != nil {
				return rootPageReadResult{}, anchorErr
			}
			decoded.Values, err = recordSortValues(result.sortKeys, anchor)
			if err != nil {
				return rootPageReadResult{}, invalidCursor()
			}
			anchorHash, err := s.cursors.privateCursorValuesHash(decoded.Values)
			if err != nil || !hmac.Equal(anchorHash, decoded.AnchorHash) {
				return rootPageReadResult{}, invalidCursor()
			}
		}
		after = &decoded
	}

	if err := charge(1); err != nil {
		return rootPageReadResult{}, err
	}
	statement, err := CompileList(collection, ListQuery{
		Filter: request.filter, Sort: request.requestedSort, After: after, Limit: request.limit,
	})
	if err != nil {
		return rootPageReadResult{}, err
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return rootPageReadResult{}, err
	}
	pageLimit := normalizedFetchLimit(request.limit) - 1
	responseBytes := 0
	hasMore := false
	for rows.Next() {
		record, err := EncodeRecord(collection, rows)
		if err != nil {
			rows.Close()
			return rootPageReadResult{}, err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			rows.Close()
			return rootPageReadResult{}, fmt.Errorf("measure record page item: %w", err)
		}
		if len(result.page.Items) > 0 && responseBytes+len(encoded) > maximumRecordPageBytes {
			hasMore = true
			break
		}
		result.page.Items = append(result.page.Items, record)
		responseBytes += len(encoded)
		if len(result.page.Items) > pageLimit {
			result.page.Items = result.page.Items[:pageLimit]
			hasMore = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return rootPageReadResult{}, err
	}
	rows.Close()

	if request.hydrate != nil {
		if err := request.hydrate(result.page.Items); err != nil {
			return rootPageReadResult{}, err
		}
	}
	var relationBytes int
	for index, record := range result.page.Items {
		encoded, err := json.Marshal(record)
		if err != nil {
			return rootPageReadResult{}, fmt.Errorf("measure hydrated record page item: %w", err)
		}
		if index > 0 && relationBytes+len(encoded) > maximumRecordPageBytes {
			result.page.Items = result.page.Items[:index]
			hasMore = true
			break
		}
		relationBytes += len(encoded)
	}
	if err := s.finalizeRecordPage(&result.page, collection.ID, result.sortKeys, result.expiresAt, hasMore); err != nil {
		return rootPageReadResult{}, err
	}
	return result, nil
}
