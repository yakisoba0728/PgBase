package cursor

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

const cursorDomain = "cursor\x00"

type Codec struct {
	secret []byte
}

type v4SortEntry struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

type v4Payload struct {
	Version      int               `json:"v"`
	CollectionID string            `json:"collectionId"`
	Sort         []v4SortEntry     `json:"sort"`
	Values       []json.RawMessage `json:"values,omitempty"`
	AnchorID     string            `json:"anchorId,omitempty"`
	AnchorHash   string            `json:"anchorHash,omitempty"`
	ExpiresAt    string            `json:"expiresAt"`
}

func NewCodec(secret []byte) *Codec {
	return &Codec{secret: append([]byte(nil), secret...)}
}

func (c *Codec) Encode(page Page) (string, error) {
	if page.Version == Version5 {
		return c.encodeV5(page)
	}
	if page.Version != Version4 || page.V5 != nil || page.CollectionID == uuid.Nil || page.ExpiresAt.IsZero() {
		return "", invalid()
	}
	if len(page.Sort) == 0 || len(page.Sort) > maxOrderTerms {
		return "", invalid()
	}
	hasValues := len(page.Values) == len(page.Sort)
	if !hasValues || len(page.AnchorHash) != 0 || (page.PrivateValues && page.AnchorID == uuid.Nil) {
		return "", invalid()
	}
	_, expiryOffset := page.ExpiresAt.Zone()
	if expiryOffset != 0 {
		return "", invalid()
	}
	sort := make([]v4SortEntry, 0, len(page.Sort))
	for _, key := range page.Sort {
		if key.Field == "" {
			return "", invalid()
		}
		sort = append(sort, v4SortEntry{Field: key.Field, Desc: key.Desc})
	}
	values := cloneValues(page.Values)
	for _, value := range values {
		if len(value) == 0 {
			return "", invalid()
		}
	}
	payloadValue := v4Payload{
		Version: Version4, CollectionID: page.CollectionID.String(), Sort: sort, Values: values,
		ExpiresAt: page.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return "", invalid()
	}
	if page.PrivateValues || len(payload)+sha256.Size > maxDecodedSize {
		if page.AnchorID == uuid.Nil {
			return "", invalid()
		}
		digest, digestErr := c.ValuesHash(values)
		if digestErr != nil {
			return "", invalid()
		}
		payloadValue.Values = nil
		payloadValue.AnchorID = page.AnchorID.String()
		payloadValue.AnchorHash = base64.RawURLEncoding.EncodeToString(digest)
		payload, err = json.Marshal(payloadValue)
		if err != nil {
			return "", invalid()
		}
	}
	return c.signAndEncode(payload)
}

func (c *Codec) Decode(token string, collectionID uuid.UUID, now time.Time) (Page, error) {
	payload, err := c.authenticatedPayload(token)
	if err != nil {
		return Page{}, err
	}
	if payloadVersion(payload) == Version5 {
		return c.decodeV5(payload, collectionID, now)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire v4Payload
	if decoder.Decode(&wire) != nil {
		return Page{}, invalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Page{}, invalid()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(canonical, payload) || wire.Version != Version4 || len(wire.Sort) == 0 || len(wire.Sort) > maxOrderTerms {
		return Page{}, invalid()
	}
	anchorID, anchorHash, hasAnchorOnly, ok := decodeAnchor(wire.AnchorID, wire.AnchorHash, len(wire.Values) == 0, false)
	if !ok || (len(wire.Values) != len(wire.Sort) && !hasAnchorOnly) || (len(wire.Values) == len(wire.Sort) && hasAnchorOnly) {
		return Page{}, invalid()
	}
	sort := make([]SortKey, len(wire.Sort))
	values := cloneValues(wire.Values)
	for index, entry := range wire.Sort {
		if entry.Field == "" || (len(values) != 0 && len(values[index]) == 0) {
			return Page{}, invalid()
		}
		sort[index] = SortKey{Field: entry.Field, Desc: entry.Desc}
	}
	payloadCollectionID, ok := canonicalUUID(wire.CollectionID)
	if !ok || payloadCollectionID != collectionID {
		return Page{}, invalid()
	}
	expiresAt, ok := canonicalExpiry(wire.ExpiresAt, now)
	if !ok {
		return Page{}, invalid()
	}
	return Page{
		Version: Version4, CollectionID: payloadCollectionID, Sort: sort, Values: values,
		AnchorID: anchorID, AnchorHash: anchorHash, PrivateValues: hasAnchorOnly, ExpiresAt: expiresAt,
	}, nil
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

func payloadVersion(payload []byte) int {
	var header struct {
		Version int `json:"v"`
	}
	if json.Unmarshal(payload, &header) != nil {
		return 0
	}
	return header.Version
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
