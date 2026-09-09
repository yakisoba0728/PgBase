package query

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/membership"
)

type rootPageReadRequest struct {
	plan          rootPlan
	cursorToken   string
	limit         int
	operationTime time.Time
	traversal     membership.Traversal

	charge   func(int) error
	hydrate  func([]map[string]any) error
	hydrator *rootHydrator
}

type rootPageReadResult struct {
	page             RecordPage
	candidates       []pageCandidate
	order            orderSpec
	expiresAt        time.Time
	totalHydrated    int
	maxRetainedRoots int
	cursorV5         *cursor.V5Descriptor
}

func (s *Service) readRootPageInTx(
	ctx context.Context,
	tx pgx.Tx,
	request rootPageReadRequest,
) (rootPageReadResult, error) {
	plan := request.plan
	order := plan.order()
	result := rootPageReadResult{
		page:      RecordPage{Items: []map[string]any{}},
		order:     order,
		expiresAt: request.operationTime.Add(cursorTTL),
	}
	charge := request.charge
	if charge == nil {
		charge = func(int) error { return nil }
	}
	if order.private() {
		if err := charge(1); err != nil {
			return rootPageReadResult{}, err
		}
		if err := enforceRelationSortRootBudget(ctx, tx, order.root); err != nil {
			return rootPageReadResult{}, err
		}
	}

	var after *pageBoundary
	if request.cursorToken != "" {
		decoded, err := s.cursors.Decode(request.cursorToken, order.root.ID, request.operationTime)
		if err != nil {
			return rootPageReadResult{}, invalidCursor()
		}
		if decoded.Version != cursor.Version5 || (order.private() && !decoded.PrivateValues) {
			return rootPageReadResult{}, invalidCursor()
		}
		membership, err := s.bindCursorV5(decoded, plan, request.traversal)
		if err != nil {
			return rootPageReadResult{}, err
		}
		result.expiresAt = decoded.ExpiresAt
		result.cursorV5 = decoded.V5
		if len(decoded.Values) == 0 {
			if err := charge(1); err != nil {
				return rootPageReadResult{}, err
			}
			values, err := order.reloadAnchor(ctx, tx, decoded.AnchorID, membership)
			if errors.Is(err, ErrRecordNotFound) {
				return rootPageReadResult{}, invalidCursor()
			}
			if err != nil {
				return rootPageReadResult{}, err
			}
			digest, err := s.cursors.ValuesHash(values)
			if err != nil || !hmac.Equal(digest, decoded.AnchorHash) {
				return rootPageReadResult{}, invalidCursor()
			}
			decoded.Values = values
		}
		after = &pageBoundary{Values: decoded.Values, membership: membership}
	} else {
		descriptor, err := s.newCursorV5Descriptor(plan, request.traversal)
		if err != nil {
			return rootPageReadResult{}, err
		}
		result.cursorV5 = descriptor
		if !order.root.IsSQLView() {
			after = &pageBoundary{membership: &descriptor.HighWater, membershipOnly: true}
		}
	}
	assembler := pageAssembler{
		cursors: s.cursors, order: order, expiresAt: result.expiresAt, maximumBytes: maximumRecordPageBytes,
		cursorV5: result.cursorV5,
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
	after *pageBoundary,
	charge func(int) error,
) (rootPageReadResult, error) {
	pageLimit := normalizedFetchLimit(request.limit) - 1
	measuredBytes := 0
	accepted := 0
	acceptedCursor := ""
	for len(result.candidates) < pageLimit {
		remaining := pageLimit - len(result.candidates)
		batchLimit := min(rootHydrationBatchSize, remaining)
		if err := charge(1); err != nil {
			return rootPageReadResult{}, err
		}
		// The preflight deliberately decodes every fetched base row so stored-value
		// errors keep their historical order. Only compact identity/cursor stubs
		// survive the callback; the full base map is immediately collectible.
		batch := make([]rootStub, 0, batchLimit+1)
		err := scanRootRows(ctx, tx, request.plan, after, batchLimit+1, func(candidate pageCandidate) (bool, error) {
			noteRetainedRoots(&result, result.candidates, 1)
			stub, err := compactRootCandidate(candidate)
			candidate.root = nil
			candidate.wire = nil
			if err != nil {
				return false, err
			}
			batch = append(batch, stub)
			return true, nil
		})
		if err != nil {
			return rootPageReadResult{}, err
		}

		hasMore := len(batch) > batchLimit
		if hasMore {
			batch[batchLimit] = rootStub{}
			batch = batch[:batchLimit:batchLimit]
		}
		if len(batch) == 0 {
			keep, cursor, err := assembler.finish(result.candidates, false)
			if err != nil {
				return rootPageReadResult{}, err
			}
			releaseRootSuffix(request.hydrator, result.candidates, keep)
			promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, keep)
			if err != nil {
				return rootPageReadResult{}, err
			}
			result.totalHydrated += promoted
			noteRetainedRoots(&result, result.candidates, 0)
			return finishRootPage(result, keep, cursor), nil
		}
		batchIndex := 0
		complete := false
		finalKeep := accepted
		finalCursor := acceptedCursor
		visited, err := request.hydrator.replaySequential(ctx, batch, func(candidate pageCandidate) (bool, error) {
			result.totalHydrated++
			noteRetainedRoots(&result, result.candidates, 1)
			result.candidates = append(result.candidates, candidate)
			noteRetainedRoots(&result, result.candidates, 0)
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
				releaseRootSuffix(request.hydrator, result.candidates, accepted)
				return true, nil
			}
			accepted = keep
			acceptedCursor = cursor
			if keep < len(result.candidates) {
				releaseRootSuffix(request.hydrator, result.candidates, keep)
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
		releaseRootSuffix(request.hydrator, result.candidates, accepted)
		promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, accepted)
		if err != nil {
			return rootPageReadResult{}, err
		}
		result.totalHydrated += promoted
		noteRetainedRoots(&result, result.candidates, 0)
		if complete {
			return finishRootPage(result, finalKeep, finalCursor), nil
		}

		last := result.candidates[len(result.candidates)-1]
		after = &pageBoundary{Values: cloneCursorValues(last.cursorValues), membership: after.membershipBoundary()}
	}
	keep, cursor, err := assembler.finish(result.candidates, true)
	if err != nil {
		return rootPageReadResult{}, err
	}
	releaseRootSuffix(request.hydrator, result.candidates, keep)
	promoted, err := request.hydrator.ensurePrefix(ctx, result.candidates, keep)
	if err != nil {
		return rootPageReadResult{}, err
	}
	result.totalHydrated += promoted
	noteRetainedRoots(&result, result.candidates, 0)
	return finishRootPage(result, keep, cursor), nil
}

func releaseRootSuffix(hydrator *rootHydrator, candidates []pageCandidate, keep int) {
	if keep < 0 {
		keep = 0
	}
	for index := keep; index < len(candidates); index++ {
		hydrator.release(&candidates[index])
	}
}

func noteRetainedRoots(result *rootPageReadResult, candidates []pageCandidate, transient int) {
	retained := transient
	for index := range candidates {
		if candidates[index].root != nil {
			retained++
		}
	}
	result.maxRetainedRoots = max(result.maxRetainedRoots, retained)
}

func scanRootRows(
	ctx context.Context,
	tx pgx.Tx,
	plan rootPlan,
	after *pageBoundary,
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
	for index := keep; index < len(result.candidates); index++ {
		result.candidates[index] = pageCandidate{}
	}
	result.candidates = result.candidates[:keep:keep]
	result.page.Items = make([]map[string]any, keep)
	result.page.NextCursor = cursor
	for index := range keep {
		result.page.Items[index] = result.candidates[index].root
	}
	return result
}
