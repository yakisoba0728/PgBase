package query

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/cursor"
)

const testMaximumDecodedCursorSize = 4096

func testCursorSort() []cursor.SortKey {
	return []cursor.SortKey{{Field: systemFieldCreatedAt}, {Field: systemFieldID}}
}

func testCursorValues() []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`),
		json.RawMessage(`"12345678-1234-4567-8901-123456789abc"`),
	}
}

func TestCursorCodecRoundTripsCanonicalVersionedPayload(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	want := cursor.Page{
		Version:      cursor.Version4,
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
	wantPayload := `{"v":` + strconv.Itoa(cursor.Version4) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56.123456789Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T12:49:56Z"}`
	if got := string(raw[:len(raw)-sha256.Size]); got != wantPayload {
		t.Fatalf("payload = %q, want %q", got, wantPayload)
	}

	got, err := codec.Decode(first, want.CollectionID, want.ExpiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Version != want.Version || got.CollectionID != want.CollectionID || !reflect.DeepEqual(got.Sort, want.Sort) || len(got.Values) != len(want.Values) {
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

func TestCursorCodecV4GoldenTokenIsByteIdenticalWithDualDecoder(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	token, err := codec.Encode(cursor.Page{
		Version:      cursor.Version4,
		CollectionID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Sort:         testCursorSort(),
		Values:       testCursorValues(),
		ExpiresAt:    time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "eyJ2Ijo0LCJjb2xsZWN0aW9uSWQiOiJhYWFhYWFhYS1iYmJiLTRjY2MtOGRkZC1lZWVlZWVlZWVlZWUiLCJzb3J0IjpbeyJmaWVsZCI6ImNyZWF0ZWRfYXQifSx7ImZpZWxkIjoiaWQifV0sInZhbHVlcyI6WyIyMDI2LTA4LTI4VDEyOjM0OjU2LjEyMzQ1Njc4OVoiLCIxMjM0NTY3OC0xMjM0LTQ1NjctODkwMS0xMjM0NTY3ODlhYmMiXSwiZXhwaXJlc0F0IjoiMjAyNi0wOC0yOFQxMjo0OTo1NloifWxYRsPOQAIV-PT37QBYtXHbot0Oq9w-tpbeyUdnAqU0"
	if token != want {
		t.Fatalf("v4 token changed:\ngot  %s\nwant %s", token, want)
	}
}

func TestCursorCodecRoundTripsCanonicalV5Descriptor(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	queryHash := bytes.Repeat([]byte{0x11}, sha256.Size)
	scopeHash := bytes.Repeat([]byte{0x22}, sha256.Size)
	want := cursor.Page{
		Version:      cursor.Version5,
		CollectionID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Values:       testCursorValues(),
		ExpiresAt:    time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
		V5: &cursor.V5Descriptor{
			CatalogVersion: 12, MembershipGeneration: 1,
			QueryHash: queryHash, ScopeHash: scopeHash, HighWater: 123,
			Order: []cursor.V5OrderTerm{
				{Path: []string{"$created_at"}, Type: "timestamptz", Nulls: cursor.NullsLast},
				{Path: []string{"$id"}, Type: "uuid", Nulls: cursor.NullsLast},
			},
		},
	}

	token, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode(v5) error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := `{"v":5,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","catalogVersion":"12","membershipGeneration":"1","queryHash":"ERERERERERERERERERERERERERERERERERERERERERE","scopeHash":"IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI","order":[{"path":["$created_at"],"type":"timestamptz","desc":false,"nulls":"last"},{"path":["$id"],"type":"uuid","desc":false,"nulls":"last"}],"highWater":"123","values":["2026-08-28T12:34:56.123456789Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T12:49:56Z"}`
	if got := string(raw[:len(raw)-sha256.Size]); got != wantPayload {
		t.Fatalf("v5 payload = %q, want %q", got, wantPayload)
	}

	got, err := codec.Decode(token, want.CollectionID, want.ExpiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Decode(v5) error = %v", err)
	}
	if got.Version != cursor.Version5 || got.CollectionID != want.CollectionID ||
		!reflect.DeepEqual(got.Values, want.Values) || !reflect.DeepEqual(got.V5, want.V5) ||
		!got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("Decode(v5) = %#v, want %#v", got, want)
	}
}

func TestCursorCodecRejectsLogicalNamesInV5StableOrder(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order[0].Path = []string{"created_at"}
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsV5OrderWithoutRootIDTiebreaker(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order[1].Path = []string{"11111111-1111-4111-8111-111111111111"}
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsUnsupportedV5OrderType(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order[0].Type = "jsonb"
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsSystemFieldAsV5RelationHop(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order[0].Path = []string{"$created_at", "$id"}
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsMismatchedV5SystemFieldType(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order[0].Type = "text"
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecPreservesRepeatedV5OrderPathsFromAcceptedSorts(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.V5.Order = append(cursor.V5.Order, cursor.V5.Order[len(cursor.V5.Order)-1])
	cursor.Values = append(cursor.Values, cursor.Values[len(cursor.Values)-1])
	token, err := codec.Encode(cursor)
	if err != nil {
		t.Fatalf("Encode(repeated stable path) error = %v", err)
	}
	decoded, err := codec.Decode(token, cursor.CollectionID, cursor.ExpiresAt.Add(-time.Minute))
	if err != nil || !reflect.DeepEqual(decoded.V5.Order, cursor.V5.Order) {
		t.Fatalf("Decode(repeated stable path) = %#v, error = %v", decoded.V5, err)
	}
}

func TestCursorCodecRejectsV5ValueThatDoesNotMatchSignedOrderType(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.Values[0] = json.RawMessage(`"not-a-timestamp"`)
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecAcceptsCanonicalV5ValuesForEveryStableOrderType(t *testing.T) {
	t.Parallel()

	tests := map[string]json.RawMessage{
		"boolean":     json.RawMessage(`true`),
		"integer":     json.RawMessage(`-2147483648`),
		"bigint":      json.RawMessage(`"9223372036854775807"`),
		"numeric":     json.RawMessage(`"123.4500"`),
		"text":        json.RawMessage(`"Ada"`),
		"timestamptz": json.RawMessage(`"2026-08-28T12:34:56.123456789Z"`),
		"uuid":        json.RawMessage(`"00000000-0000-0000-0000-000000000000"`),
	}
	for fieldType, value := range tests {
		fieldType, value := fieldType, value
		t.Run(fieldType, func(t *testing.T) {
			t.Parallel()
			page := validV5CursorForTest()
			page.V5.Order[0] = cursor.V5OrderTerm{
				Path: []string{"11111111-1111-4111-8111-111111111111"}, Type: fieldType, Nulls: cursor.NullsLast,
			}
			page.Values[0] = value
			if _, err := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef")).Encode(page); err != nil {
				t.Fatalf("Encode(%s) error = %v", fieldType, err)
			}
		})
	}
}

func TestCursorCodecRejectsNoncanonicalOrUnsafeV5TextValues(t *testing.T) {
	t.Parallel()

	for _, value := range []json.RawMessage{json.RawMessage(`"\u0061"`), json.RawMessage(`"a\u0000b"`)} {
		page := validV5CursorForTest()
		page.V5.Order[0] = cursor.V5OrderTerm{
			Path: []string{"11111111-1111-4111-8111-111111111111"}, Type: "text", Nulls: cursor.NullsLast,
		}
		page.Values[0] = value
		_, err := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef")).Encode(page)
		assertInvalidCursor(t, err)
	}
}

func TestCursorCodecV5AnchorOnlyRoundTripPreservesAbsoluteExpiry(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	want := validV5CursorForTest()
	want.AnchorID = uuid.MustParse("99999999-9999-4999-8999-999999999999")
	want.PrivateValues = true
	token, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode(v5 anchor-only) error = %v", err)
	}
	decoded, err := codec.Decode(token, want.CollectionID, want.ExpiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Decode(v5 anchor-only) error = %v", err)
	}
	if decoded.Version != cursor.Version5 || !decoded.PrivateValues || len(decoded.Values) != 0 ||
		decoded.AnchorID != want.AnchorID || len(decoded.AnchorHash) != sha256.Size ||
		!decoded.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("decoded v5 anchor-only cursor = %#v", decoded)
	}
	reissued, err := codec.Encode(decoded)
	if err != nil {
		t.Fatalf("re-encode decoded v5 anchor-only cursor: %v", err)
	}
	if reissued != token {
		t.Fatalf("v5 continuation changed signed token or absolute expiry:\nfirst  %s\nsecond %s", token, reissued)
	}
}

func TestCursorCodecRejectsMixedV4AndV5DescriptorFields(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	cursor := validV5CursorForTest()
	cursor.Sort = testCursorSort()
	_, err := codec.Encode(cursor)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsV5DescriptorOnV4Cursor(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	page := cursor.Page{
		Version: cursor.Version4, CollectionID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Sort: testCursorSort(), Values: testCursorValues(),
		ExpiresAt: time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
		V5:        validV5CursorForTest().V5,
	}
	_, err := codec.Encode(page)
	assertInvalidCursor(t, err)
}

func TestCursorCodecRejectsUninitializedV5Fingerprints(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	for _, clear := range []func(*cursor.V5Descriptor){
		func(descriptor *cursor.V5Descriptor) { descriptor.QueryHash = make([]byte, sha256.Size) },
		func(descriptor *cursor.V5Descriptor) { descriptor.ScopeHash = make([]byte, sha256.Size) },
	} {
		cursor := validV5CursorForTest()
		clear(cursor.V5)
		_, err := codec.Encode(cursor)
		assertInvalidCursor(t, err)
	}
}

func TestCursorCodecRejectsAuthenticatedMalformedV5PayloadsGenerically(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := cursor.NewCodec(secret)
	want := validV5CursorForTest()
	token, err := codec.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw[:len(raw)-sha256.Size])
	anchorID := "99999999-9999-4999-8999-999999999999"
	anchorHash := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, sha256.Size))
	zeroHash := base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
	tests := map[string]string{
		"unknown field":           strings.Replace(base, `,"expiresAt":`, `,"unknown":true,"expiresAt":`, 1),
		"noncanonical whitespace": "{ " + base[1:],
		"noncanonical decimal":    strings.Replace(base, `"highWater":"123"`, `"highWater":"0123"`, 1),
		"omitted direction":       strings.Replace(base, `,"desc":false`, "", 1),
		"mixed values and anchor": strings.Replace(base, `,"expiresAt":`, `,"anchorId":"`+anchorID+`","anchorHash":"`+anchorHash+`","expiresAt":`, 1),
		"v4 sort field":           strings.Replace(base, `,"expiresAt":`, `,"sort":[{"field":"id"}],"expiresAt":`, 1),
		"zero query fingerprint":  strings.Replace(base, base64.RawURLEncoding.EncodeToString(want.V5.QueryHash), zeroHash, 1),
		"trailing json":           base + `{}`,
	}
	for name, payload := range tests {
		name, payload := name, payload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.Decode(authenticatedCursorFixture(secret, []byte(payload)), want.CollectionID, want.ExpiresAt.Add(-time.Minute))
			assertInvalidCursor(t, err)
		})
	}
}

func validV5CursorForTest() cursor.Page {
	return cursor.Page{
		Version:      cursor.Version5,
		CollectionID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Values:       testCursorValues(),
		ExpiresAt:    time.Date(2026, 8, 28, 12, 49, 56, 0, time.UTC),
		V5: &cursor.V5Descriptor{
			CatalogVersion: 12, MembershipGeneration: 1,
			QueryHash: bytes.Repeat([]byte{0x11}, sha256.Size),
			ScopeHash: bytes.Repeat([]byte{0x22}, sha256.Size), HighWater: 123,
			Order: []cursor.V5OrderTerm{
				{Path: []string{"$created_at"}, Type: "timestamptz", Nulls: cursor.NullsLast},
				{Path: []string{"$id"}, Type: "uuid", Nulls: cursor.NullsLast},
			},
		},
	}
}

func TestCursorCodecFallsBackToBoundedSignedAnchorForLargeValues(t *testing.T) {
	t.Parallel()

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	anchorID := uuid.MustParse("12345678-1234-4567-8901-123456789abc")
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(cursor.Page{
		Version: cursor.Version4, CollectionID: collectionID,
		Sort: []cursor.SortKey{{Field: "title"}, {Field: systemFieldID}},
		Values: []json.RawMessage{
			json.RawMessage(strconv.Quote(strings.Repeat("x", testMaximumDecodedCursorSize))),
			json.RawMessage(strconv.Quote(anchorID.String())),
		},
		AnchorID: anchorID, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(token) > base64.RawURLEncoding.EncodedLen(testMaximumDecodedCursorSize) {
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
	codec := cursor.NewCodec(secret)
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(cursor.Page{
		Version: cursor.Version4, CollectionID: collectionID,
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
		codec *cursor.Codec
		token string
	}{
		{name: "payload bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(tamperedPayload)},
		{name: "signature bit flip", codec: codec, token: base64.RawURLEncoding.EncodeToString(tamperedSignature)},
		{name: "truncation", codec: codec, token: base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1])},
		{name: "signature only", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))},
		{name: "bad base64", codec: codec, token: "%%%"},
		{name: "padding rejected", codec: codec, token: token + "="},
		{name: "empty", codec: codec, token: ""},
		{name: "secret rotation", codec: cursor.NewCodec([]byte("abcdef0123456789abcdef0123456789")), token: token},
		{name: "oversize", codec: codec, token: base64.RawURLEncoding.EncodeToString(make([]byte, testMaximumDecodedCursorSize+2))},
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
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.New()
	expiresAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	token, err := codec.Encode(cursor.Page{
		Version: cursor.Version4, CollectionID: collectionID,
		Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(token, uuid.New(), expiresAt.Add(-time.Minute)); !cursor.IsInvalid(err) {
		t.Fatalf("cross-collection cursor error = %v", err)
	}
	if _, err := codec.Decode(token, collectionID, expiresAt); !cursor.IsInvalid(err) {
		t.Fatalf("expired cursor error = %v", err)
	}
}

func TestCursorCodecRejectsAuthenticatedInvalidPayloads(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := cursor.NewCodec(secret)
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 40, 0, 0, time.UTC)
	base := `{"v":` + strconv.Itoa(cursor.Version4) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T13:00:00Z"}`
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unsupported version", payload: strings.Replace(base, `"v":`+strconv.Itoa(cursor.Version4), `"v":`+strconv.Itoa(cursor.Version4-1), 1)},
		{name: "missing sort", payload: `{"v":` + strconv.Itoa(cursor.Version4) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","values":["1"],"expiresAt":"2026-08-28T13:00:00Z"}`},
		{name: "empty sort", payload: `{"v":` + strconv.Itoa(cursor.Version4) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[],"values":[],"expiresAt":"2026-08-28T13:00:00Z"}`},
		{name: "value count mismatch", payload: strings.Replace(base, `,"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"]`, `,"values":["2026-08-28T12:34:56Z"]`, 1)},
		{name: "empty sort field", payload: strings.Replace(base, `"field":"created_at"`, `"field":""`, 1)},
		{name: "invalid collection", payload: strings.Replace(base, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "not-a-uuid", 1)},
		{name: "non utc expiry", payload: strings.Replace(base, "2026-08-28T13:00:00Z", "2026-08-28T22:00:00+09:00", 1)},
		{name: "unknown field", payload: strings.Replace(base, `,"expiresAt":`, `,"role":"admin","expiresAt":`, 1)},
		{name: "wrong type", payload: strings.Replace(base, `"v":`+strconv.Itoa(cursor.Version4), `"v":"`+strconv.Itoa(cursor.Version4)+`"`, 1)},
		{name: "malformed json", payload: `{"v":` + strconv.Itoa(cursor.Version4)},
		{name: "trailing json", payload: base + `{}`},
		{name: "non canonical json", payload: `{ "v":` + strconv.Itoa(cursor.Version4) + `,"collectionId":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sort":[{"field":"created_at"},{"field":"id"}],"values":["2026-08-28T12:34:56Z","12345678-1234-4567-8901-123456789abc"],"expiresAt":"2026-08-28T13:00:00Z"}`},
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

	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Minute)
	tests := []struct {
		name   string
		cursor cursor.Page
	}{
		{name: "unsupported version", cursor: cursor.Page{Version: 2, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "nil collection", cursor: cursor.Page{Version: cursor.Version4, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "no sort", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Values: testCursorValues(), ExpiresAt: expiresAt}},
		{name: "sort without values", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Sort: testCursorSort(), ExpiresAt: expiresAt}},
		{name: "empty sort value", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Sort: testCursorSort(), Values: []json.RawMessage{nil, json.RawMessage(`"x"`)}, ExpiresAt: expiresAt}},
		{name: "caller supplied anchor hash", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), AnchorHash: make([]byte, sha256.Size), ExpiresAt: expiresAt}},
		{name: "non utc time", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues(), ExpiresAt: time.Date(2026, 8, 28, 21, 0, 0, 0, time.FixedZone("KST", 9*60*60))}},
		{name: "zero time", cursor: cursor.Page{Version: cursor.Version4, CollectionID: collectionID, Sort: testCursorSort(), Values: testCursorValues()}},
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

func TestCursorCodecCopiesSecret(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	codec := cursor.NewCodec(secret)
	collectionID := uuid.New()
	page := cursor.Page{
		Version: cursor.Version4, CollectionID: collectionID,
		Sort: testCursorSort(), Values: testCursorValues(),
		ExpiresAt: time.Date(2026, 8, 28, 12, 15, 0, 0, time.UTC),
	}
	token, err := codec.Encode(page)
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
	if err == nil || !cursor.IsInvalid(err) {
		t.Fatalf("error = %v, want typed invalid cursor", err)
	}
	if err.Error() != "invalid cursor" {
		t.Fatalf("error text = %q, want generic invalid cursor", err.Error())
	}
}
