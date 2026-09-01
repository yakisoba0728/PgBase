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

	charge   func(int) error
	hydrate  func([]map[string]any) error
	hydrator *rootHydrator
}

type rootPageReadResult struct {
	page                RecordPage
	candidates          []pageCandidate
	sortKeys            []SortKey
	expiresAt           time.Time
	privateCursor       bool
	totalHydrated       int
	maxRetainedHydrated int
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
	assembler := pageAssembler{
		cursors: s.cursors, collectionID: plan.collection.ID, sortKeys: plan.sortKeys,
		private: plan.privateCursor, expiresAt: result.expiresAt, maximumBytes: maximumRecordPageBytes,
	}
	if request.hydrator != nil {
		return s.readSequentialRootPage(ctx, tx, request, result, assembler, after, charge)
	}

	if err := charge(1); err != nil {
		return rootPageReadResult{}, err
	}
	pageLimit := normalizedFetchLimit(request.limit) - 1
	responseBytes := 0
	hasMore := false
	err := scanRootRows(ctx, tx, plan, after, pageLimit+1, func(candidate pageCandidate) (bool, error) {
		encoded, err := json.Marshal(candidate.root)
		if err != nil {
			return false, fmt.Errorf("measure record page item: %w", err)
		}
		if len(result.candidates) > 0 && responseBytes+len(encoded) > maximumRecordPageBytes {
			hasMore = true
			return false, nil
		}
		result.candidates = append(result.candidates, candidate)
		responseBytes += len(encoded)
		if len(result.candidates) > pageLimit {
			result.candidates[pageLimit] = pageCandidate{}
			result.candidates = result.candidates[:pageLimit:pageLimit]
			hasMore = true
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return rootPageReadResult{}, err
	}

	if request.hydrate != nil {
		roots := make([]map[string]any, len(result.candidates))
		for index := range result.candidates {
			roots[index] = result.candidates[index].root
		}
		if err := request.hydrate(roots); err != nil {
			return rootPageReadResult{}, err
		}
	}

	keep, cursor, err := assembler.finish(result.candidates, hasMore)
	if err != nil {
		return rootPageReadResult{}, err
	}
	return finishRootPage(result, keep, cursor), nil
}

func (s *Service) readSequentialRootPage(
	ctx context.Context,
	tx pgx.Tx,
	request rootPageReadRequest,
	result rootPageReadResult,
	assembler pageAssembler,
	after *PageCursor,
	charge func(int) error,
) (rootPageReadResult, error) {
	pageLimit := normalizedFetchLimit(request.limit) - 1
	retainedHydrated := 0
	measuredBytes := 0
	accepted := 0
	acceptedCursor := ""
	for len(result.candidates) < pageLimit {
		remaining := pageLimit - len(result.candidates)
		batchLimit := min(rootHydrationBatchSize, remaining)
		if err := charge(1); err != nil {
			return rootPageReadResult{}, err
		}
		// Decode one fixed base batch before opening the relation-row stream. This
		// preserves the existing base scan/error order while bounding speculative
		// base representations independently of hydrated root retention.
		batch := make([]pageCandidate, 0, batchLimit+1)
		err := scanRootRows(ctx, tx, request.plan, after, batchLimit+1, func(candidate pageCandidate) (bool, error) {
			batch = append(batch, candidate)
			return true, nil
		})
		if err != nil {
			return rootPageReadResult{}, err
		}

		hasMore := len(batch) > batchLimit
		if hasMore {
			batch[batchLimit] = pageCandidate{}
			batch = batch[:batchLimit:batchLimit]
		}
		if len(batch) == 0 {
			keep, cursor, err := assembler.finish(result.candidates, false)
			if err != nil {
				return rootPageReadResult{}, err
			}
			promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, keep)
			if err != nil {
				return rootPageReadResult{}, err
			}
			result.totalHydrated += promoted
			retainedHydrated += promoted
			result.maxRetainedHydrated = max(result.maxRetainedHydrated, retainedHydrated)
			return finishRootPage(result, keep, cursor), nil
		}
		batchIndex := 0
		complete := false
		finalKeep := accepted
		finalCursor := acceptedCursor
		visited, err := request.hydrator.hydrateSequential(ctx, batch, func(candidate *pageCandidate) (bool, error) {
			result.totalHydrated++
			result.candidates = append(result.candidates, *candidate)
			retainedHydrated++
			result.maxRetainedHydrated = max(result.maxRetainedHydrated, retainedHydrated)
			batchIndex++
			candidateHasMore := batchIndex < len(batch) || hasMore
			current := &result.candidates[len(result.candidates)-1]
			itemBytes, err := current.measureWire()
			if err != nil {
				return false, err
			}
			measuredBytes += itemBytes
			keep, cursor, err := assembler.finish(result.candidates, candidateHasMore)
			if err != nil {
				_, minimumBytes, minimumErr := assembler.measureResponse(result.candidates, measuredBytes, false)
				if minimumErr != nil {
					return false, minimumErr
				}
				if !candidateHasMore || !isResponseTooLarge(err) || minimumBytes > assembler.maximumBytes {
					return false, err
				}
				request.hydrator.release(current)
				retainedHydrated--
				return true, nil
			}
			accepted = keep
			acceptedCursor = cursor
			if keep < len(result.candidates) {
				request.hydrator.release(current)
				retainedHydrated--
				_, minimumBytes, err := assembler.measureResponse(result.candidates, measuredBytes, false)
				if err != nil {
					return false, err
				}
				if minimumBytes > assembler.maximumBytes {
					finalKeep, finalCursor = keep, cursor
					complete = true
					return false, nil
				}
			}
			if !candidateHasMore || len(result.candidates) == pageLimit {
				finalKeep, finalCursor = keep, cursor
				complete = true
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return rootPageReadResult{}, err
		}
		if !complete && visited != len(batch) {
			return rootPageReadResult{}, errors.New("hydrate relation values: incomplete batch")
		}
		promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, accepted)
		if err != nil {
			return rootPageReadResult{}, err
		}
		result.totalHydrated += promoted
		retainedHydrated += promoted
		result.maxRetainedHydrated = max(result.maxRetainedHydrated, retainedHydrated)
		if complete {
			return finishRootPage(result, finalKeep, finalCursor), nil
		}

		last := result.candidates[len(result.candidates)-1]
		after = &PageCursor{Values: cloneCursorValues(last.cursorValues)}
	}
	keep, cursor, err := assembler.finish(result.candidates, true)
	if err != nil {
		return rootPageReadResult{}, err
	}
	promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, keep)
	if err != nil {
		return rootPageReadResult{}, err
	}
	result.totalHydrated += promoted
	retainedHydrated += promoted
	result.maxRetainedHydrated = max(result.maxRetainedHydrated, retainedHydrated)
	return finishRootPage(result, keep, cursor), nil
}

func scanRootRows(
	ctx context.Context,
	tx pgx.Tx,
	plan rootPlan,
	after *PageCursor,
	fetchLimit int,
	visit func(pageCandidate) (bool, error),
) error {
	statement, err := plan.pageStatement(after, fetchLimit)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		candidate, err := plan.scan(rows)
		if err != nil {
			return err
		}
		keepGoing, err := visit(candidate)
		if err != nil {
			return err
		}
		if !keepGoing {
			rows.Close()
			return rows.Err()
		}
	}
	return rows.Err()
}

func isResponseTooLarge(err error) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr) && len(validationErr.Details) == 1 &&
		validationErr.Details[0] == (ValidationDetail{Field: "response", Code: "response_too_large"})
}

func finishRootPage(result rootPageReadResult, keep int, cursor string) rootPageReadResult {
	result.candidates = result.candidates[:keep]
	result.page.Items = make([]map[string]any, keep)
	result.page.NextCursor = cursor
	for index := range keep {
		result.page.Items[index] = result.candidates[index].root
	}
	return result
}
