package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const cursorDomain = "cursor\x00"

type Codec struct {
	secret []byte
}

func NewCodec(secret []byte) *Codec {
	return &Codec{secret: append([]byte(nil), secret...)}
}

func (c *Codec) ValuesHash(values []json.RawMessage) ([]byte, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return c.fingerprint("cursor-values\x00", encoded), nil
}

func (c *Codec) QueryFingerprint(canonicalDescriptor []byte) []byte {
	return c.fingerprint("cursor-query-v1\x00", canonicalDescriptor)
}

func (c *Codec) ScopeFingerprint(canonicalDescriptor []byte) []byte {
	return c.fingerprint("cursor-scope-v1\x00", canonicalDescriptor)
}

func (c *Codec) authenticatedPayload(token string) ([]byte, error) {
	if token == "" || len(token) > base64.RawURLEncoding.EncodedLen(maxDecodedSize) {
		return nil, invalid()
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size || len(raw) > maxDecodedSize {
		return nil, invalid()
	}
	payload := raw[:len(raw)-sha256.Size]
	if !hmac.Equal(raw[len(raw)-sha256.Size:], c.fingerprint(cursorDomain, payload)) {
		return nil, invalid()
	}
	return payload, nil
}

func (c *Codec) signAndEncode(payload []byte) (string, error) {
	signature := c.fingerprint(cursorDomain, payload)
	if len(payload)+len(signature) > maxDecodedSize {
		return "", invalid()
	}
	raw := append(append(make([]byte, 0, len(payload)+len(signature)), payload...), signature...)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *Codec) fingerprint(domain string, payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func decodeAnchor(rawID, rawHash string, valuesEmpty, requireNonzeroHash bool) (uuid.UUID, []byte, bool, bool) {
	id := uuid.Nil
	if rawID != "" {
		var ok bool
		id, ok = canonicalUUID(rawID)
		if !ok {
			return uuid.Nil, nil, false, false
		}
	}
	hash := []byte(nil)
	if rawHash != "" {
		var ok bool
		hash, ok = decodeHash(rawHash, requireNonzeroHash)
		if !ok {
			return uuid.Nil, nil, false, false
		}
	}
	anchorOnly := valuesEmpty && id != uuid.Nil && len(hash) == sha256.Size
	if (!valuesEmpty && (id != uuid.Nil || len(hash) != 0)) || (valuesEmpty && !anchorOnly) {
		return uuid.Nil, nil, false, false
	}
	return id, hash, anchorOnly, true
}

func decodeHash(value string, requireNonzero bool) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	valid := len(decoded) == sha256.Size && (!requireNonzero || validDigest(decoded))
	return decoded, err == nil && valid && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func canonicalUUID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	return id, err == nil && id != uuid.Nil && id.String() == raw
}

func canonicalExpiry(raw string, now time.Time) (time.Time, bool) {
	expiresAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || raw != expiresAt.UTC().Format(time.RFC3339Nano) || !expiresAt.After(now.UTC()) {
		return time.Time{}, false
	}
	_, offset := expiresAt.Zone()
	return expiresAt.UTC(), offset == 0
}

func cloneValues(values []json.RawMessage) []json.RawMessage {
	result := make([]json.RawMessage, len(values))
	for index := range values {
		result[index] = append(json.RawMessage(nil), values[index]...)
	}
	return result
}
