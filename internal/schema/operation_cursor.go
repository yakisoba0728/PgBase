package schema

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	schemaOperationCursorVersion            = 1
	schemaOperationCursorTTL                = 15 * time.Minute
	schemaOperationCursorClockSkew          = 30 * time.Second
	minimumSchemaOperationCursorSecretBytes = 32
	schemaOperationCursorMACDomain          = "pgbase-schema-operation-cursor-v1"
)

var errInvalidSchemaOperationCursor = errors.New("invalid schema operation cursor")

type schemaOperationCursorAnchor struct {
	CreatedAt   time.Time
	OperationID uuid.UUID
}

type schemaOperationCursorCodec struct {
	secret []byte
}

type schemaOperationCursorPayload struct {
	Version     int    `json:"v"`
	OwnerID     string `json:"ownerId"`
	CreatedAt   string `json:"createdAt"`
	OperationID string `json:"operationId"`
	IssuedAt    string `json:"issuedAt"`
	ExpiresAt   string `json:"expiresAt"`
}

func newSchemaOperationCursorCodec(secret []byte) *schemaOperationCursorCodec {
	return &schemaOperationCursorCodec{secret: append([]byte(nil), secret...)}
}

func (c *schemaOperationCursorCodec) encode(
	ownerID uuid.UUID,
	anchor schemaOperationCursorAnchor,
	issuedAt time.Time,
) (string, error) {
	if c == nil || len(c.secret) < minimumSchemaOperationCursorSecretBytes ||
		ownerID == uuid.Nil || anchor.OperationID == uuid.Nil ||
		anchor.CreatedAt.IsZero() || issuedAt.IsZero() {
		return "", errInvalidSchemaOperationCursor
	}
	createdAt := anchor.CreatedAt.UTC()
	issuedAt = issuedAt.UTC()
	expiresAt := issuedAt.Add(schemaOperationCursorTTL)
	if createdAt.Year() < 1 || createdAt.Year() > 9999 ||
		issuedAt.Year() < 1 || issuedAt.Year() > 9999 ||
		expiresAt.Year() < 1 || expiresAt.Year() > 9999 {
		return "", errInvalidSchemaOperationCursor
	}
	payload, err := json.Marshal(schemaOperationCursorPayload{
		Version: schemaOperationCursorVersion, OwnerID: ownerID.String(),
		CreatedAt: createdAt.Format(time.RFC3339Nano), OperationID: anchor.OperationID.String(),
		IssuedAt: issuedAt.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", errInvalidSchemaOperationCursor
	}
	signature := c.sign(ownerID, payload)
	raw := make([]byte, 0, len(payload)+len(signature))
	raw = append(raw, payload...)
	raw = append(raw, signature...)
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) > maximumOperationCursorEncodedBytes {
		return "", errInvalidSchemaOperationCursor
	}
	return token, nil
}

func (c *schemaOperationCursorCodec) decode(
	token string,
	ownerID uuid.UUID,
	now time.Time,
) (schemaOperationCursorAnchor, error) {
	if c == nil || len(c.secret) < minimumSchemaOperationCursorSecretBytes ||
		ownerID == uuid.Nil || token == "" ||
		len(token) > maximumOperationCursorEncodedBytes {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token || len(raw) <= sha256.Size {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	payloadBytes := raw[:len(raw)-sha256.Size]
	signature := raw[len(raw)-sha256.Size:]
	if !hmac.Equal(signature, c.sign(ownerID, payloadBytes)) {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}

	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	var payload schemaOperationCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) || payload.Version != schemaOperationCursorVersion {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}

	payloadOwnerID, err := uuid.Parse(payload.OwnerID)
	if err != nil || payloadOwnerID == uuid.Nil || payloadOwnerID.String() != payload.OwnerID || payloadOwnerID != ownerID {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	operationID, err := uuid.Parse(payload.OperationID)
	if err != nil || operationID == uuid.Nil || operationID.String() != payload.OperationID {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	createdAt, ok := parseCanonicalSchemaOperationCursorTime(payload.CreatedAt)
	if !ok {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	issuedAt, ok := parseCanonicalSchemaOperationCursorTime(payload.IssuedAt)
	if !ok {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	expiresAt, ok := parseCanonicalSchemaOperationCursorTime(payload.ExpiresAt)
	if !ok || expiresAt.Sub(issuedAt) != schemaOperationCursorTTL {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	now = now.UTC()
	if issuedAt.After(now.Add(schemaOperationCursorClockSkew)) || !expiresAt.After(now) {
		return schemaOperationCursorAnchor{}, errInvalidSchemaOperationCursor
	}
	return schemaOperationCursorAnchor{CreatedAt: createdAt, OperationID: operationID}, nil
}

func parseCanonicalSchemaOperationCursorTime(encoded string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	if err != nil || parsed.Year() < 1 || parsed.Year() > 9999 ||
		parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != encoded {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func (c *schemaOperationCursorCodec) sign(ownerID uuid.UUID, payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	writeSchemaOperationDigestPart(mac, []byte(schemaOperationCursorMACDomain))
	writeSchemaOperationDigestPart(mac, ownerID[:])
	writeSchemaOperationDigestPart(mac, payload)
	return mac.Sum(nil)
}
