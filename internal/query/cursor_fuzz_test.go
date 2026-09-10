package query

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cursorwire "github.com/yakisoba0728/PgBase/internal/cursor"
)

func FuzzCursorDecode(f *testing.F) {
	codec := cursorwire.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	keyset := validV5CursorForTest()
	keyset.ExpiresAt = now.Add(time.Minute)
	valid, err := codec.Encode(keyset)
	if err != nil {
		f.Fatal(err)
	}
	anchored := validV5CursorForTest()
	anchored.ExpiresAt = now.Add(time.Minute)
	anchored.AnchorID = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	anchored.PrivateValues = true
	anchorOnly, err := codec.Encode(anchored)
	if err != nil {
		f.Fatal(err)
	}
	// The retired v4 token is a seed rather than a fixture: it is correctly
	// signed, so it exercises the path where a well-formed envelope carries a
	// format this binary no longer knows.
	for _, seed := range []string{
		"", "%%%", valid, anchorOnly, retiredV4GoldenToken, valid + "=", "eyJ2IjoyfQ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		cursor, err := codec.Decode(token, collectionID, now)
		if err != nil {
			if !cursorwire.IsInvalid(err) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if cursor.CollectionID != collectionID || cursor.Version != cursorwire.Version5 ||
			cursor.V5 == nil || !cursor.ExpiresAt.After(now) {
			t.Fatalf("invalid decoded cursor: %#v", cursor)
		}
		orderLength := len(cursor.V5.Order)
		valuesShape := len(cursor.Values) == orderLength
		anchorOnlyShape := cursor.PrivateValues && len(cursor.Values) == 0 &&
			cursor.AnchorID != uuid.Nil && len(cursor.AnchorHash) == sha256.Size
		if orderLength == 0 || (!valuesShape && !anchorOnlyShape) {
			t.Fatalf("invalid decoded cursor shape: %#v", cursor)
		}
	})
}

func FuzzFilterParse(f *testing.F) {
	for _, seed := range []string{
		"", "title = 'x'", "(a=1&&b=2)||c!=null", "1=1", "title = 'unterminated", "(a=1",
		"created_at >= '2026-01-01T00:00:00Z'", "id = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'",
		"flag = true", "x" + strings.Repeat("&&x=1", 500), "title='''quote'",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		node, err := parseFilter(expression)
		if err != nil {
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || len(validationErr.Details) == 0 {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if node == nil && expression != "" {
			t.Fatalf("empty node for non-empty input: %q", expression)
		}
	})
}
