package userauth

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestEmailOTPIDCarriesAuthenticatedAccountTag(t *testing.T) {
	s := &Service{secret: []byte("0123456789abcdef0123456789abcdef")}
	collection, other := uuid.New(), uuid.New()
	first, err := s.newEmailOTPID(collection, "a@example.test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.newEmailOTPID(collection, "a@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.Version() != 4 || first.Variant() != uuid.RFC4122 {
		t.Fatalf("ids %s %s", first, second)
	}
	firstTag, ok := s.emailOTPIDTag(collection, first)
	secondTag, ok2 := s.emailOTPIDTag(collection, second)
	if !ok || !ok2 || !bytes.Equal(firstTag, secondTag) || !bytes.Equal(firstTag, s.emailOTPAccountTag(collection, "a@example.test")) {
		t.Fatal("ids for one address do not share an authenticated tag")
	}
	elsewhere, err := s.newEmailOTPID(collection, "b@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if tag, _ := s.emailOTPIDTag(collection, elsewhere); bytes.Equal(tag, firstTag) {
		t.Fatal("different addresses share a tag")
	}
	if _, ok := s.emailOTPIDTag(other, first); ok {
		t.Fatal("id authenticated in another collection")
	}
	forged := first
	forged[7] ^= 1
	if _, ok := s.emailOTPIDTag(collection, forged); ok {
		t.Fatal("modified id still authenticated")
	}
	if _, ok := s.emailOTPIDTag(collection, uuid.New()); ok {
		t.Fatal("random id authenticated")
	}
}
