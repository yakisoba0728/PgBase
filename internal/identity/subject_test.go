package identity

import (
	"testing"

	"github.com/google/uuid"
)

func TestSubjectValid(t *testing.T) {
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")
	tests := []struct {
		name    string
		subject Subject
		want    bool
	}{
		{name: "administrator", subject: Subject{Kind: Administrator, ID: administratorID}, want: true},
		{name: "missing kind", subject: Subject{ID: administratorID}},
		{name: "unsupported kind", subject: Subject{Kind: "public", ID: administratorID}},
		{name: "missing ID", subject: Subject{Kind: Administrator}},
		{name: "zero value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.subject.Valid(); got != test.want {
				t.Fatalf("Subject.Valid() = %t, want %t", got, test.want)
			}
		})
	}
}
