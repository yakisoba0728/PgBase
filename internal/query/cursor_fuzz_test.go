package query

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func FuzzCursorDecode(f *testing.F) {
	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	valid, err := codec.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID, CreatedAt: now,
		ID: uuid.MustParse("12345678-1234-4567-8901-123456789abc"), ExpiresAt: now.Add(time.Minute),
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
	})
}
