package query

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cursorwire "github.com/webisnoteasy/PgBase/internal/cursor"
)

func FuzzCursorDecode(f *testing.F) {
	codec := cursorwire.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	valid, err := codec.Encode(cursorwire.Page{
		Version: cursorwire.Version4, CollectionID: collectionID,
		Sort:      testCursorSort(),
		Values:    testCursorValues(),
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		f.Fatal(err)
	}
	anchorOnly, err := codec.Encode(cursorwire.Page{
		Version: cursorwire.Version4, CollectionID: collectionID,
		Sort:          testCursorSort(),
		Values:        testCursorValues(),
		AnchorID:      uuid.MustParse("99999999-9999-4999-8999-999999999999"),
		PrivateValues: true,
		ExpiresAt:     now.Add(time.Minute),
	})
	if err != nil {
		f.Fatal(err)
	}
	v5 := validV5CursorForTest()
	v5.ExpiresAt = now.Add(time.Minute)
	v5.AnchorID = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	v5.PrivateValues = true
	v5AnchorOnly, err := codec.Encode(v5)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"", "%%%", valid, anchorOnly, v5AnchorOnly, valid + "=", "eyJ2IjoyfQ"} {
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
		if cursor.CollectionID != collectionID ||
			(cursor.Version != cursorwire.Version4 && cursor.Version != cursorwire.Version5) || !cursor.ExpiresAt.After(now) {
			t.Fatalf("invalid decoded cursor: %#v", cursor)
		}
		orderLength := len(cursor.Sort)
		if cursor.Version == cursorwire.Version5 {
			if cursor.V5 == nil {
				t.Fatalf("v5 cursor omitted descriptor: %#v", cursor)
			}
			orderLength = len(cursor.V5.Order)
		}
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
