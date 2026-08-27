package query

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorCodecRoundTripsCanonicalVersionedPayload(t *testing.T) {
	t.Parallel()

	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	want := PageCursor{
		Version:   1,
		CreatedAt: time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC),
		ID:        uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	}

	first, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	second, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode() second error = %v", err)
	}
	if first != second {
		t.Fatalf("Encode() was non-deterministic: %q != %q", first, second)
	}
	if strings.Contains(first, "=") {
		t.Fatalf("Encode() token %q contains base64 padding", first)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode token fixture: %v", err)
	}
	wantPayload := `{"v":1,"createdAt":"2026-08-28T12:34:56.123456789Z","id":"12345678-1234-4567-8901-123456789abc"}`
	if got := string(raw[:len(raw)-sha256.Size]); got != wantPayload {
		t.Fatalf("payload = %q, want %q", got, wantPayload)
	}

	got, err := codec.Decode(first)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Version != want.Version || !got.CreatedAt.Equal(want.CreatedAt) || got.CreatedAt.Location() != time.UTC || got.ID != want.ID {
		t.Fatalf("Decode() = %#v, want %#v in UTC", got, want)
	}
}

func TestCursorCodecRejectsTamperingMalformedTokensAndSecretRotation(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := NewCursorCodec(secret)
	token, err := codec.Encode(PageCursor{
		Version:   1,
		CreatedAt: time.Date(2026, 8, 28, 12, 34, 56, 0, time.UTC),
		ID:        uuid.MustParse("12345678-1234-4567-8901-123456789abc"),
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token fixture: %v", err)
	}
	tamperedPayload := append([]byte(nil), raw...)
	tamperedPayload[5] ^= 1
	tamperedSignature := append([]byte(nil), raw...)
	tamperedSignature[len(tamperedSignature)-1] ^= 1

	tests := []struct {
		name  string
		codec *CursorCodec
		token string
	}{
		{name: "payload bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(tamperedPayload)},
		{name: "signature bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(tamperedSignature)},
		{name: "truncation", codec: codec, token: base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1])},
		{name: "signature only", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))},
		{name: "bad base64", codec: codec, token: "%%%"},
		{name: "padding rejected", codec: codec, token: token + "="},
		{name: "empty", codec: codec, token: ""},
		{name: "secret rotation", codec: NewCursorCodec([]byte("abcdef0123456789abcdef0123456789")), token: token},
		{name: "oversize", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, 513))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.codec.Decode(test.token)
			assertInvalidCursor(t, err)
		})
	}
}

func TestCursorCodecRejectsAuthenticatedInvalidPayloads(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := NewCursorCodec(secret)
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unsupported version", payload: `{"v":2,"createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc"}`},
		{name: "missing version", payload: `{"createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc"}`},
		{name: "invalid uuid", payload: `{"v":1,"createdAt":"2026-08-28T12:34:56Z","id":"not-a-uuid"}`},
		{name: "non utc time", payload: `{"v":1,"createdAt":"2026-08-28T21:34:56+09:00","id":"12345678-1234-4567-8901-123456789abc"}`},
		{name: "timestamp without offset", payload: `{"v":1,"createdAt":"2026-08-28T12:34:56","id":"12345678-1234-4567-8901-123456789abc"}`},
		{name: "unknown field", payload: `{"v":1,"createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc","role":"admin"}`},
		{name: "wrong type", payload: `{"v":"1","createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc"}`},
		{name: "malformed json", payload: `{"v":1`},
		{name: "trailing json", payload: `{"v":1,"createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc"}{}`},
		{name: "non canonical json", payload: `{ "v":1,"createdAt":"2026-08-28T12:34:56Z","id":"12345678-1234-4567-8901-123456789abc"}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.Decode(authenticatedCursorFixture(secret, []byte(test.payload)))
			assertInvalidCursor(t, err)
		})
	}
}

func TestCursorCodecRejectsInvalidValuesBeforeEncoding(t *testing.T) {
	t.Parallel()

	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	tests := []struct {
		name   string
		cursor PageCursor
	}{
		{name: "unsupported version", cursor: PageCursor{Version: 2, CreatedAt: time.Now().UTC(), ID: uuid.New()}},
		{name: "nil uuid", cursor: PageCursor{Version: 1, CreatedAt: time.Now().UTC(), ID: uuid.Nil}},
		{name: "non utc time", cursor: PageCursor{Version: 1, CreatedAt: time.Date(2026, 8, 28, 21, 0, 0, 0, time.FixedZone("KST", 9*60*60)), ID: uuid.New()}},
		{name: "zero time", cursor: PageCursor{Version: 1, ID: uuid.New()}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.Encode(test.cursor)
			assertInvalidCursor(t, err)
		})
	}
}

func TestNewCursorCodecCopiesSecret(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := NewCursorCodec(secret)
	cursor := PageCursor{Version: 1, CreatedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), ID: uuid.New()}
	token, err := codec.Encode(cursor)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	for index := range secret {
		secret[index] = 0
	}
	if _, err := codec.Decode(token); err != nil {
		t.Fatalf("Decode() after caller secret mutation error = %v", err)
	}
}

func authenticatedCursorFixture(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("cursor\x00"))
	_, _ = mac.Write(payload)
	raw := append(append([]byte(nil), payload...), mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func assertInvalidCursor(t *testing.T, err error) {
	t.Helper()
	if err == nil || !IsInvalidCursor(err) {
		t.Fatalf("error = %v, want typed invalid cursor", err)
	}
	if err.Error() != "invalid cursor" {
		t.Fatalf("error text = %q, want generic invalid cursor", err.Error())
	}
}
