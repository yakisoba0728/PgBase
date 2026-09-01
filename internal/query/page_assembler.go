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
	hydrated     bool
	wireBytes    int
	wireMeasured bool
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

	measured := 0
	for index := range keep {
		itemBytes, err := candidates[index].measureWire()
		if err != nil {
			return 0, "", err
		}
		measured += itemBytes
	}
	for {
		cursor, responseBytes, err := a.measureResponse(candidates[:keep], measured, hasMore)
		if err != nil {
			return 0, "", err
		}
		if responseBytes <= a.maximumBytes {
			return keep, cursor, nil
		}
		if keep <= 1 {
			return 0, "", validation("response", "response_too_large")
		}
		keep--
		measured -= candidates[keep].wireBytes
		hasMore = true
	}
}

func (c *pageCandidate) measureWire() (int, error) {
	if c.wireMeasured {
		return c.wireBytes, nil
	}
	encoded, err := json.Marshal(c.wire)
	if err != nil {
		return 0, fmt.Errorf("measure record page response: %w", err)
	}
	c.wireBytes = len(encoded)
	c.wireMeasured = true
	return c.wireBytes, nil
}

func (a pageAssembler) measureResponse(candidates []pageCandidate, measured int, hasMore bool) (string, int, error) {
	cursor, err := a.cursor(candidates, hasMore)
	if err != nil {
		return "", 0, err
	}
	envelope, err := json.Marshal(RecordPage{Items: []map[string]any{}, NextCursor: cursor})
	if err != nil {
		return "", 0, fmt.Errorf("measure record page response: %w", err)
	}
	responseBytes := len(envelope) + measured + 1
	if len(candidates) > 0 {
		responseBytes += len(candidates) - 1
	}
	return cursor, responseBytes, nil
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
