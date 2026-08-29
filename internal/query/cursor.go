package query

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	cursorVersion        = 3
	maxDecodedCursorSize = 4096
	cursorDomain         = "cursor\x00"
	maximumCursorSortLen = maximumSortKeys + 1
)

type CursorCodec struct {
	secret []byte
}

type cursorSortEntry struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

type cursorPayload struct {
	Version      int               `json:"v"`
	CollectionID string            `json:"collectionId"`
	Sort         []cursorSortEntry `json:"sort"`
	Values       []json.RawMessage `json:"values"`
	ExpiresAt    string            `json:"expiresAt"`
}

func NewCursorCodec(secret []byte) *CursorCodec {
	return &CursorCodec{secret: append([]byte(nil), secret...)}
}

func (c *CursorCodec) Encode(cursor PageCursor) (string, error) {
	if cursor.Version != cursorVersion || cursor.CollectionID == uuid.Nil || cursor.ExpiresAt.IsZero() {
		return "", invalidCursor()
	}
	if len(cursor.Sort) == 0 || len(cursor.Sort) > maximumCursorSortLen || len(cursor.Values) != len(cursor.Sort) {
		return "", invalidCursor()
	}
	_, expiryOffset := cursor.ExpiresAt.Zone()
	if expiryOffset != 0 {
		return "", invalidCursor()
	}
	sort := make([]cursorSortEntry, 0, len(cursor.Sort))
	for _, key := range cursor.Sort {
		if key.Field == "" {
			return "", invalidCursor()
		}
		sort = append(sort, cursorSortEntry{Field: key.Field, Desc: key.Desc})
	}
	values := make([]json.RawMessage, 0, len(cursor.Values))
	for _, value := range cursor.Values {
		if len(value) == 0 {
			return "", invalidCursor()
		}
		values = append(values, append(json.RawMessage(nil), value...))
	}
	payload, err := json.Marshal(cursorPayload{
		Version:      cursor.Version,
		CollectionID: cursor.CollectionID.String(),
		Sort:         sort,
		Values:       values,
		ExpiresAt:    cursor.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", invalidCursor()
	}
	signature := c.sign(payload)
	if len(payload)+len(signature) > maxDecodedCursorSize {
		return "", invalidCursor()
	}
	raw := make([]byte, 0, len(payload)+len(signature))
	raw = append(raw, payload...)
	raw = append(raw, signature...)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *CursorCodec) Decode(token string, collectionID uuid.UUID, now time.Time) (PageCursor, error) {
	if token == "" || len(token) > base64.RawURLEncoding.EncodedLen(maxDecodedCursorSize) {
		return PageCursor{}, invalidCursor()
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size || len(raw) > maxDecodedCursorSize {
		return PageCursor{}, invalidCursor()
	}
	payloadBytes := raw[:len(raw)-sha256.Size]
	signature := raw[len(raw)-sha256.Size:]
	if !hmac.Equal(signature, c.sign(payloadBytes)) {
		return PageCursor{}, invalidCursor()
	}

	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	var payload cursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return PageCursor{}, invalidCursor()
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return PageCursor{}, invalidCursor()
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) || payload.Version != cursorVersion {
		return PageCursor{}, invalidCursor()
	}
	if len(payload.Sort) == 0 || len(payload.Sort) > maximumCursorSortLen || len(payload.Values) != len(payload.Sort) {
		return PageCursor{}, invalidCursor()
	}
	sort := make([]SortKey, 0, len(payload.Sort))
	values := make([]json.RawMessage, 0, len(payload.Values))
	for index, entry := range payload.Sort {
		if entry.Field == "" {
			return PageCursor{}, invalidCursor()
		}
		sort = append(sort, SortKey{Field: entry.Field, Desc: entry.Desc})
		if len(payload.Values[index]) == 0 {
			return PageCursor{}, invalidCursor()
		}
		values = append(values, append(json.RawMessage(nil), payload.Values[index]...))
	}
	payloadCollectionID, err := uuid.Parse(payload.CollectionID)
	if err != nil || payloadCollectionID == uuid.Nil || payloadCollectionID.String() != payload.CollectionID || payloadCollectionID != collectionID {
		return PageCursor{}, invalidCursor()
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, payload.ExpiresAt)
	if err != nil || payload.ExpiresAt != expiresAt.UTC().Format(time.RFC3339Nano) || !expiresAt.After(now.UTC()) {
		return PageCursor{}, invalidCursor()
	}
	_, expiryOffset := expiresAt.Zone()
	if expiryOffset != 0 {
		return PageCursor{}, invalidCursor()
	}
	return PageCursor{
		Version: cursorVersion, CollectionID: payloadCollectionID,
		Sort: sort, Values: values, ExpiresAt: expiresAt.UTC(),
	}, nil
}

func (c *CursorCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(cursorDomain))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
