package query

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func assertEquivalentCursorIgnoringExpiry(
	t *testing.T,
	records *Service,
	collectionID uuid.UUID,
	gotToken string,
	wantToken string,
) {
	t.Helper()
	if gotToken == "" || wantToken == "" {
		if gotToken != wantToken {
			t.Fatalf("cursor presence differs: got=%q want=%q", gotToken, wantToken)
		}
		return
	}
	got, err := records.cursors.Decode(gotToken, collectionID, time.Time{})
	if err != nil {
		t.Fatalf("decode got cursor: %v", err)
	}
	want, err := records.cursors.Decode(wantToken, collectionID, time.Time{})
	if err != nil {
		t.Fatalf("decode want cursor: %v", err)
	}
	got.ExpiresAt = time.Time{}
	want.ExpiresAt = time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cursor bindings differ:\n got: %#v\nwant: %#v", got, want)
	}
}
