package query

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func FuzzCursorDecode(f *testing.F) {
	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	valid, err := codec.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort:      testCursorSort(),
		Values:    testCursorValues(),
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"", "%%%", valid, valid + "=", "eyJ2IjoyfQ"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		cursor, err := codec.Decode(token, collectionID, now)
		if err != nil {
			if !IsInvalidCursor(err) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if cursor.CollectionID != collectionID || cursor.Version != cursorVersion || !cursor.ExpiresAt.After(now) {
			t.Fatalf("invalid decoded cursor: %#v", cursor)
		}
		if len(cursor.Sort) == 0 || len(cursor.Values) != len(cursor.Sort) {
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
