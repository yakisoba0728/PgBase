package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type pageCandidate struct {
	root         map[string]any
	wire         any
	cursorValues []json.RawMessage
	oversized    bool
}

type pageAssembler struct {
	cursors      *CursorCodec
	collectionID uuid.UUID
	sortKeys     []SortKey
	private      bool
	expiresAt    time.Time
	maximumBytes int
}

func (a pageAssembler) finish(candidates []pageCandidate, hasMore bool) (int, string, error) {
	keep := len(candidates)
	for index := range candidates {
		if !candidates[index].oversized {
			continue
		}
		if index == 0 {
			return 0, "", validation("response", "response_too_large")
		}
		keep = index
		hasMore = true
		break
	}

	itemBytes := make([]int, keep)
	measured := 0
	for index := range keep {
		encoded, err := json.Marshal(candidates[index].wire)
		if err != nil {
			return 0, "", fmt.Errorf("measure record page response: %w", err)
		}
		itemBytes[index] = len(encoded)
		measured += len(encoded)
	}

	for {
		cursor, err := a.cursor(candidates[:keep], hasMore)
		if err != nil {
			return 0, "", err
		}
		envelope, err := json.Marshal(RecordPage{Items: []map[string]any{}, NextCursor: cursor})
		if err != nil {
			return 0, "", fmt.Errorf("measure record page response: %w", err)
		}
		responseBytes := len(envelope) + measured
		if keep > 0 {
			responseBytes += keep - 1
		}
		if responseBytes+1 <= a.maximumBytes {
			return keep, cursor, nil
		}
		if keep <= 1 {
			return 0, "", validation("response", "response_too_large")
		}
		keep--
		measured -= itemBytes[keep]
		hasMore = true
	}
}

func (a pageAssembler) cursor(candidates []pageCandidate, hasMore bool) (string, error) {
	if !hasMore || len(candidates) == 0 {
		return "", nil
	}
	anchor := candidates[len(candidates)-1]
	anchorID, err := canonicalRecordID(anchor.root)
	if err != nil {
		return "", errors.New("encode page cursor: invalid record id")
	}
	if len(anchor.cursorValues) != len(a.sortKeys) {
		return "", errors.New("encode page cursor: missing sort value")
	}
	return a.cursors.Encode(PageCursor{
		Version:       cursorVersion,
		CollectionID:  a.collectionID,
		Sort:          a.sortKeys,
		Values:        cloneCursorValues(anchor.cursorValues),
		AnchorID:      anchorID,
		PrivateValues: a.private,
		ExpiresAt:     a.expiresAt,
	})
}
