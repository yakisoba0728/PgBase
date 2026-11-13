package usermigrate

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedSourceErrorIsPostgresText(t *testing.T) {
	for _, message := range []string{
		strings.Repeat("a", 1023) + "한",
		strings.Repeat("한", 400),
		"invalid\xfffilename\x00",
		"\x00\xff",
	} {
		got := boundedSourceError(errors.New(message))
		if got == "" || len(got) > 1024 || !utf8.ValidString(got) || strings.ContainsRune(got, '\x00') {
			t.Errorf("invalid stored error: bytes=%d validUTF8=%t message=%q", len(got), utf8.ValidString(got), got)
		}
	}
}
