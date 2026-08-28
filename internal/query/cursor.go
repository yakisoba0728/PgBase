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
	cursorVersion        = 2
	maxDecodedCursorSize = 512
	cursorDomain         = "cursor\x00"
)

type CursorCodec struct {
	secret []byte
}

type cursorPayload struct {
	Version      int    `json:"v"`
	CollectionID string `json:"collectionId"`
	CreatedAt    string `json:"createdAt"`
	ID           string `json:"id"`
	ExpiresAt    string `json:"expiresAt"`
}

func NewCursorCodec(secret []byte) *CursorCodec {
	return &CursorCodec{secret: append([]byte(nil), secret...)}
}

func (c *CursorCodec) Encode(cursor PageCursor) (string, error) {
	if cursor.Version != cursorVersion || cursor.CollectionID == uuid.Nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil || cursor.ExpiresAt.IsZero() {
		return "", invalidCursor()
	}
	_, createdOffset := cursor.CreatedAt.Zone()
	_, expiryOffset := cursor.ExpiresAt.Zone()
	if createdOffset != 0 || expiryOffset != 0 {
		return "", invalidCursor()
	}
	payload, err := json.Marshal(cursorPayload{
		Version:      cursor.Version,
		CollectionID: cursor.CollectionID.String(),
		CreatedAt:    cursor.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:           cursor.ID.String(),
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
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || payload.CreatedAt != createdAt.UTC().Format(time.RFC3339Nano) {
		return PageCursor{}, invalidCursor()
	}
	_, offset := createdAt.Zone()
	if offset != 0 {
		return PageCursor{}, invalidCursor()
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil || id == uuid.Nil || id.String() != payload.ID {
		return PageCursor{}, invalidCursor()
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
		CreatedAt: createdAt.UTC(), ID: id, ExpiresAt: expiresAt.UTC(),
	}, nil
}

func (c *CursorCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(cursorDomain))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
