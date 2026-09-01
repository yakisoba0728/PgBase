package query

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testCursorSort() []SortKey {
	return []SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}}
}

func testCursorValues() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`),
		json.RawMessage(`"12345678-1234-4567-8901-123456789abc"`),
	}
}

func TestCursorCodecRoundTripsCanonicalVersionedPayload(t *testing.T) {
	t.Parallel()

	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	want := PageCursor{
		Version:      cursorVersion,
		CollectionID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Sort:         testCursorSort(),
		Values:       testCursorValues(),
		ExpiresAt:    time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
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
	wantPayload := `{"v":` + strconv.Itoa(cursorVersion) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56.123456789Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T12:49:56Z"}`
	if got := string(raw[:len(raw)-sha256.Size]); got != wantPayload {
		t.Fatalf("payload = %q, want %q", got, wantPayload)
	}

	got, err := codec.Decode(first, want.CollectionID, want.ExpiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Version != want.Version || got.CollectionID != want.CollectionID || !sameSortKeys(got.Sort, want.Sort) || len(got.Values) != len(want.Values) {
		t.Fatalf("Decode() = %#v, want %#v in UTC", got, want)
	}
	for index := range want.Values {
		if string(got.Values[index]) != string(want.Values[index]) {
			t.Fatalf("value %d = %s, want %s", index, got.Values[index], want.Values[index])
		}
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) || got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expiry = %#v, want %#v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestCursorCodecFallsBackToBoundedSignedAnchorForLargeValues(t *testing.T) {
	t.Parallel()

	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	anchorID := uuid.MustParse("12345678-1234-4567-8901-123456789abc")
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort: []SortKey{{Field: "title"}, {Field: systemFieldID}},
		Values: []json.RawMessage{
			json.RawMessage(strconv.Quote(strings.Repeat("x", maxDecodedCursorSize))),
			json.RawMessage(strconv.Quote(anchorID.String())),
		},
		AnchorID: anchorID, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(token) > base64.RawURLEncoding.EncodedLen(maxDecodedCursorSize) {
		t.Fatalf("fallback cursor length = %d, exceeds bound", len(token))
	}
	decoded, err := codec.Decode(token, collectionID, expiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.AnchorID != anchorID || len(decoded.Values) != 0 || len(decoded.AnchorHash) != sha256.Size {
		t.Fatalf("Decode() = %#v, want anchor-only fallback", decoded)
	}
}

func TestCursorCodecRejectsTamperingMalformedTokensAndSecretRotation(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := NewCursorCodec(secret)
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt,
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
		{name: "oversize", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, maxDecodedCursorSize+2))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.codec.Decode(test.token, collectionID, expiresAt.Add(-time.Minute))
			assertInvalidCursor(t, err)
		})
	}
}

func TestCursorCodecRejectsCrossCollectionReplayAndExpiration(t *testing.T) {
	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.New()
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(token, uuid.New(), expiresAt.Add(-time.Minute)); !IsInvalidCursor(err) {
		t.Fatalf("cross-collection cursor error = %v", err)
	}
	if _, err := codec.Decode(token, collectionID, expiresAt); !IsInvalidCursor(err) {
		t.Fatalf("expired cursor error = %v", err)
	}
}

func TestCursorCodecRejectsAuthenticatedInvalidPayloads(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := NewCursorCodec(secret)
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 40, 0, 0, time.UTC)
	base := `{"v":` + strconv.Itoa(cursorVersion) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T13:00:00Z"}`
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unsupported version", payload: strings.Replace(base, `"v":`+strconv.Itoa(cursorVersion), `"v":`+strconv.Itoa(cursorVersion-1), 1)},
		{name: "missing sort", payload: `{"v":` + strconv.Itoa(cursorVersion) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","values":["1"],"expiresAt":"2026-08-28T13:00:00Z"}`},
		{name: "empty sort", payload: `{"v":` + strconv.Itoa(cursorVersion) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[],"values":[],"expiresAt":"2026-08-28T13:00:00Z"}`},
		{name: "value count mismatch", payload: strings.Replace(base, `,"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"]`, `,"values":["2026-08-28T12:34:56Z"]`, 1)},
		{name: "empty sort field", payload: strings.Replace(base, `"field":"created_at"`, `"field":""`, 1)},
		{name: "invalid collection", payload: strings.Replace(base, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "not-a-uuid", 1)},
		{name: "non utc expiry", payload: strings.Replace(base, "2026-08-28T13:00:00Z", "2026-08-28T22:00:00+09:00", 1)},
		{name: "unknown field", payload: strings.Replace(base, `,"expiresAt":`, `,"role":"admin","expiresAt":`, 1)},
		{name: "wrong type", payload: strings.Replace(base, `"v":`+strconv.Itoa(cursorVersion), `"v":"`+strconv.Itoa(cursorVersion)+`"`, 1)},
		{name: "malformed json", payload: `{"v":` + strconv.Itoa(cursorVersion)},
		{name: "trailing json", payload: base + `{}`},
		{name: "non canonical json", payload: `{ "v":` + strconv.Itoa(cursorVersion) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T13:00:00Z"}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.Decode(authenticatedCursorFixture(secret, []byte(test.payload)), collectionID, now)
			assertInvalidCursor(t, err)
		})
	}
}

func TestCursorCodecRejectsInvalidValuesBeforeEncoding(t *testing.T) {
	t.Parallel()

	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Minute)
	tests := []struct {
		name   string
		cursor PageCursor
	}{
		{name: "unsupported version", cursor: PageCursor{Version: 2, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "nil collection", cursor: PageCursor{Version: cursorVersion, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "no sort", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "sort without values", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Sort: testCursorSort(), ExpiresAt: expiresAt}},
		{name: "empty sort value", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Sort: testCursorSort(), Values: []json.RawMessage{nil, json.RawMessage(`"x"`)}, ExpiresAt: expiresAt}},
		{name: "caller supplied anchor hash", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), AnchorHash: make([]byte, sha256.Size), ExpiresAt: expiresAt}},
		{name: "non utc time", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: time.Date(2026, 8, 28, 21, 0, 0, 0, time.FixedZone("KST", 9*60*60))}},
		{name: "zero time", cursor: PageCursor{Version: cursorVersion, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues()}},
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
	collectionID := uuid.New()
	cursor := PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort: testCursorSort(), Values: testCursorValues(),
		ExpiresAt: time.Date(2026, 8, 28, 12, 15, 0, 0, time.UTC),
	}
	token, err := codec.Encode(cursor)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	for index := range secret {
		secret[index] = 0
	}
	if _, err := codec.Decode(token, collectionID, time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC)); err != nil {
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
