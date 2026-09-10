package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
)

type pageCandidate struct {
	id           uuid.UUID
	root         map[string]any
	wire         any
	cursorValues []json.RawMessage
	oversized    bool
	hydrated     bool
	wireBytes    int
	wireMeasured bool
}

type pageAssembler struct {
	cursors      *cursor.Codec
	order        orderSpec
	expiresAt    time.Time
	maximumBytes int
	cursorV5     *cursor.V5Descriptor
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
	return a.order.encodeCursor(a.cursors, candidates[len(candidates)-1], a.expiresAt, a.cursorV5)
}

func (s orderSpec) encodeCursor(codec *cursor.Codec, anchor pageCandidate, expiresAt time.Time, v5 *cursor.V5Descriptor) (string, error) {
	if anchor.id == uuid.Nil || len(anchor.cursorValues) != len(s.terms) {
		return "", errors.New("encode page cursor: invalid anchor")
	}
	page := cursor.Page{
		Version: cursor.Version5, CollectionID: s.root.ID, Values: cloneCursorValues(anchor.cursorValues),
		AnchorID: anchor.id, PrivateValues: s.private(), ExpiresAt: expiresAt, V5: v5,
	}
	token, err := codec.Encode(page)
	if err != nil {
		return "", invalidCursor()
	}
	return token, nil
}
