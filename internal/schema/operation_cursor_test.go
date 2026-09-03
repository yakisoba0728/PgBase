package schema

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSchemaOperationCursorRoundTripsTheStableOwnerScopedAnchor(t *testing.T) {
	t.Parallel()

	codec := newSchemaOperationCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	ownerID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	anchor := schemaOperationCursorAnchor{
		CreatedAt:   time.Date(2026, 9, 2, 12, 34, 56, 123456789, time.UTC),
		OperationID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}
	issuedAt := time.Date(2026, 9, 2, 12, 40, 0, 0, time.UTC)

	first, err := codec.encode(ownerID, anchor, issuedAt)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	second, err := codec.encode(ownerID, anchor, issuedAt)
	if err != nil {
		t.Fatalf("encode cursor again: %v", err)
	}
	if first != second {
		t.Fatalf("cursor encoding was not deterministic: %q != %q", first, second)
	}
	if len(first) > maximumOperationCursorEncodedBytes {
		t.Fatalf("encoded cursor length = %d, exceeds %d", len(first), maximumOperationCursorEncodedBytes)
	}
	if strings.Contains(first, "=") {
		t.Fatalf("cursor %q contains base64 padding", first)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode cursor fixture: %v", err)
	}
	wantPayload := `{"v":1,"ownerId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","createdAt":"2026-09-02T12:34:56.123456789Z","operationId":"12345678-1234-4567-8901-123456789abc","issuedAt":"2026-09-02T12:40:00Z","expiresAt":"2026-09-02T12:55:00Z"}`
	if got := string(raw[:len(raw)-sha256.Size]); got != wantPayload {
		t.Fatalf("cursor payload = %q, want %q", got, wantPayload)
	}

	decoded, err := codec.decode(first, ownerID, issuedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if decoded.OperationID != anchor.OperationID || !decoded.CreatedAt.Equal(anchor.CreatedAt) || decoded.CreatedAt.Location() != time.UTC {
		t.Fatalf("decoded anchor = %#v, want %#v in UTC", decoded, anchor)
	}
}

func TestSchemaOperationCursorRejectsTamperingOwnerReuseAndExpiration(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := newSchemaOperationCursorCodec(secret)
	ownerID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	issuedAt := time.Date(2026, 9, 2, 12, 40, 0, 0, time.UTC)
	token, err := codec.encode(ownerID, schemaOperationCursorAnchor{
		CreatedAt:   issuedAt.Add(-time.Minute),
		OperationID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	payloadTamper := append([]byte(nil), raw...)
	payloadTamper[5] ^= 1
	signatureTamper := append([]byte(nil), raw...)
	signatureTamper[len(signatureTamper)-1] ^= 1

	tests := []struct {
		name    string
		codec   *schemaOperationCursorCodec
		token   string
		ownerID uuid.UUID
		now     time.Time
	}{
		{name: "payload bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(payloadTamper), ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "signature bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(signatureTamper), ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "different owner", codec: codec, token: token, ownerID: uuid.MustParse("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"), now: issuedAt.Add(time.Minute)},
		{name: "expired", codec: codec, token: token, ownerID: ownerID, now: issuedAt.Add(schemaOperationCursorTTL)},
		{name: "secret rotation", codec: newSchemaOperationCursorCodec([]byte("abcdef0123456789abcdef0123456789")), token: token, ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "truncated", codec: codec, token: base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1]), ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "signature only", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size)), ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "bad base64", codec: codec, token: "%%%", ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "padding", codec: codec, token: token + "=", ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "empty", codec: codec, token: "", ownerID: ownerID, now: issuedAt.Add(time.Minute)},
		{name: "oversize", codec: codec, token: strings.Repeat("a", maximumOperationCursorEncodedBytes+1), ownerID: ownerID, now: issuedAt.Add(time.Minute)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.codec.decode(test.token, test.ownerID, test.now)
			assertInvalidSchemaOperationCursor(t, err)
		})
	}
}

func TestSchemaOperationCursorAllowsOnlyBoundedIssuerClockSkew(t *testing.T) {
	t.Parallel()

	codec := newSchemaOperationCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	ownerID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	issuedAt := time.Date(2026, 9, 2, 12, 40, 0, 0, time.UTC)
	allowedClockSkew := 30 * time.Second
	token, err := codec.encode(ownerID, schemaOperationCursorAnchor{
		CreatedAt:   issuedAt.Add(-time.Minute),
		OperationID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.decode(token, ownerID, issuedAt.Add(-allowedClockSkew)); err != nil {
		t.Fatalf("decode at clock-skew boundary: %v", err)
	}
	_, err = codec.decode(token, ownerID, issuedAt.Add(-allowedClockSkew-time.Nanosecond))
	assertInvalidSchemaOperationCursor(t, err)
}

func TestSchemaOperationCursorRejectsAuthenticatedInvalidPayloads(t *testing.T) {
	t.Parallel()

	codec := newSchemaOperationCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	ownerID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 9, 2, 12, 41, 0, 0, time.UTC)
	base := `{"v":1,"ownerId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","createdAt":"2026-09-02T12:34:56Z","operationId":"12345678-1234-4567-8901-123456789abc","issuedAt":"2026-09-02T12:40:00Z","expiresAt":"2026-09-02T12:55:00Z"}`
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unsupported version", payload: strings.Replace(base, `"v":1`, `"v":2`, 1)},
		{name: "payload owner mismatch", payload: strings.Replace(base, ownerID.String(), "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff", 1)},
		{name: "nil operation", payload: strings.Replace(base, "12345678-1234-4567-8901-123456789abc", uuid.Nil.String(), 1)},
		{name: "invalid operation", payload: strings.Replace(base, "12345678-1234-4567-8901-123456789abc", "not-a-uuid", 1)},
		{name: "non UTC created", payload: strings.Replace(base, "2026-09-02T12:34:56Z", "2026-09-02T21:34:56+09:00", 1)},
		{name: "future issuance", payload: strings.Replace(base, "2026-09-02T12:40:00Z", "2026-09-02T12:42:00Z", 1)},
		{name: "wrong lifetime", payload: strings.Replace(base, "2026-09-02T12:55:00Z", "2026-09-02T12:56:00Z", 1)},
		{name: "unknown member", payload: strings.Replace(base, `,"expiresAt"`, `,"role":"admin","expiresAt"`, 1)},
		{name: "wrong type", payload: strings.Replace(base, `"v":1`, `"v":"1"`, 1)},
		{name: "malformed JSON", payload: `{"v":1`},
		{name: "trailing JSON", payload: base + `{}`},
		{name: "non canonical JSON", payload: " " + base},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			token := authenticatedSchemaOperationCursorFixture(codec, ownerID, []byte(test.payload))
			_, err := codec.decode(token, ownerID, now)
			assertInvalidSchemaOperationCursor(t, err)
		})
	}
}

func TestSchemaOperationCursorCodecCopiesItsSecret(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := newSchemaOperationCursorCodec(secret)
	ownerID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	issuedAt := time.Date(2026, 9, 2, 12, 40, 0, 0, time.UTC)
	token, err := codec.encode(ownerID, schemaOperationCursorAnchor{
		CreatedAt: issuedAt, OperationID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	for index := range secret {
		secret[index] = 0
	}
	if _, err := codec.decode(token, ownerID, issuedAt.Add(time.Minute)); err != nil {
		t.Fatalf("decode after caller secret mutation: %v", err)
	}
}

func TestSchemaOperationCursorRejectsInvalidEncodingInputs(t *testing.T) {
	t.Parallel()

	validCodec := newSchemaOperationCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	validOwner := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	validTime := time.Date(2026, 9, 2, 12, 40, 0, 0, time.UTC)
	validAnchor := schemaOperationCursorAnchor{
		CreatedAt:   validTime,
		OperationID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}
	tests := []struct {
		name     string
		codec    *schemaOperationCursorCodec
		ownerID  uuid.UUID
		anchor   schemaOperationCursorAnchor
		issuedAt time.Time
	}{
		{name: "nil codec", ownerID: validOwner, anchor: validAnchor, issuedAt: validTime},
		{name: "short secret", codec: newSchemaOperationCursorCodec([]byte("short")), ownerID: validOwner, anchor: validAnchor, issuedAt: validTime},
		{name: "nil owner", codec: validCodec, anchor: validAnchor, issuedAt: validTime},
		{name: "nil operation", codec: validCodec, ownerID: validOwner, anchor: schemaOperationCursorAnchor{CreatedAt: validTime}, issuedAt: validTime},
		{name: "zero creation time", codec: validCodec, ownerID: validOwner, anchor: schemaOperationCursorAnchor{OperationID: validAnchor.OperationID}, issuedAt: validTime},
		{name: "zero issuance time", codec: validCodec, ownerID: validOwner, anchor: validAnchor},
		{name: "expiry outside RFC3339", codec: validCodec, ownerID: validOwner, anchor: validAnchor, issuedAt: time.Date(9999, 12, 31, 23, 50, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.codec.encode(test.ownerID, test.anchor, test.issuedAt)
			assertInvalidSchemaOperationCursor(t, err)
		})
	}

	validToken, err := validCodec.encode(validOwner, validAnchor, validTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSchemaOperationCursorCodec([]byte("short")).decode(
		validToken, validOwner, validTime.Add(time.Minute),
	); err == nil {
		t.Fatal("short-secret codec authenticated a cursor")
	}
}

func authenticatedSchemaOperationCursorFixture(
	codec *schemaOperationCursorCodec,
	ownerID uuid.UUID,
	payload []byte,
) string {
	raw := append(append([]byte(nil), payload...), codec.sign(ownerID, payload)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func assertInvalidSchemaOperationCursor(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errInvalidSchemaOperationCursor) {
		t.Fatalf("error = %v, want invalid schema operation cursor", err)
	}
	if err.Error() != "invalid schema operation cursor" {
		t.Fatalf("error text = %q, want generic cursor error", err.Error())
	}
}
