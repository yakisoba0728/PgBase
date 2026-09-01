package query

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type rootPageReadRequest struct {
	plan          rootPlan
	cursorToken   string
	limit         int
	operationTime time.Time

	charge  func(int) error
	hydrate func([]map[string]any) error
}

type rootPageReadResult struct {
	page          RecordPage
	candidates    []pageCandidate
	sortKeys      []SortKey
	expiresAt     time.Time
	privateCursor bool
}

func (s *Service) readRootPageInTx(
	ctx context.Context,
	tx pgx.Tx,
	request rootPageReadRequest,
) (rootPageReadResult, error) {
	plan := request.plan
	result := rootPageReadResult{
		page:          RecordPage{Items: []map[string]any{}},
		sortKeys:      plan.sortKeys,
		expiresAt:     request.operationTime.Add(cursorTTL),
		privateCursor: plan.privateCursor,
	}
	charge := request.charge
	if charge == nil {
		charge = func(int) error { return nil }
	}
	if plan.privateCursor {
		if err := charge(1); err != nil {
			return rootPageReadResult{}, err
		}
		if err := enforceRelationSortRootBudget(ctx, tx, plan.collection); err != nil {
			return rootPageReadResult{}, err
		}
	}

	var after *PageCursor
	if request.cursorToken != "" {
		decoded, err := s.cursors.Decode(request.cursorToken, plan.collection.ID, request.operationTime)
		if err != nil || !sameSortKeys(decoded.Sort, plan.sortKeys) || (plan.privateCursor && !decoded.PrivateValues) {
			return rootPageReadResult{}, invalidCursor()
		}
		if len(decoded.Values) == 0 {
			if err := charge(1); err != nil {
				return rootPageReadResult{}, err
			}
			values, err := plan.anchorValues(ctx, tx, decoded.AnchorID)
			if errors.Is(err, ErrRecordNotFound) {
				return rootPageReadResult{}, invalidCursor()
			}
			if err != nil {
				return rootPageReadResult{}, err
			}
			digest, err := s.cursors.privateCursorValuesHash(values)
			if err != nil || !hmac.Equal(digest, decoded.AnchorHash) {
				return rootPageReadResult{}, invalidCursor()
			}
			decoded.Values = values
		}
		after = &decoded
	}

	if err := charge(1); err != nil {
		return rootPageReadResult{}, err
	}
	statement, err := plan.statement(after, request.limit)
	if err != nil {
		return rootPageReadResult{}, err
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return rootPageReadResult{}, err
	}
	defer rows.Close()

	pageLimit := normalizedFetchLimit(request.limit) - 1
	responseBytes := 0
	hasMore := false
	for rows.Next() {
		candidate, err := plan.scan(rows)
		if err != nil {
			return rootPageReadResult{}, err
		}
		encoded, err := json.Marshal(candidate.root)
		if err != nil {
			return rootPageReadResult{}, fmt.Errorf("measure record page item: %w", err)
		}
		if len(result.candidates) > 0 && responseBytes+len(encoded) > maximumRecordPageBytes {
			hasMore = true
			break
		}
		result.candidates = append(result.candidates, candidate)
		responseBytes += len(encoded)
		if len(result.candidates) > pageLimit {
			result.candidates = result.candidates[:pageLimit]
			hasMore = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return rootPageReadResult{}, err
	}
	rows.Close()

	if request.hydrate != nil {
		roots := make([]map[string]any, len(result.candidates))
		for index := range result.candidates {
			roots[index] = result.candidates[index].root
		}
		if err := request.hydrate(roots); err != nil {
			return rootPageReadResult{}, err
		}
	}

	assembler := pageAssembler{
		cursors: s.cursors, collectionID: plan.collection.ID, sortKeys: plan.sortKeys,
		private: plan.privateCursor, expiresAt: result.expiresAt, maximumBytes: maximumRecordPageBytes,
	}
	keep, cursor, err := assembler.finish(result.candidates, hasMore)
	if err != nil {
		return rootPageReadResult{}, err
	}
	result.candidates = result.candidates[:keep]
	result.page.Items = make([]map[string]any, keep)
	result.page.NextCursor = cursor
	for index := range keep {
		candidate := result.candidates[index]
		result.page.Items[index] = candidate.root
	}
	return result, nil
}
