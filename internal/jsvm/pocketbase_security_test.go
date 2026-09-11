package jsvm

import (
	"regexp"
	"testing"
)

func TestRandomByRegexSupportsPocketBaseOperatorsWithinBounds(t *testing.T) {
	for _, pattern := range []string{`(?:ab|cd)[A-Z]{2,4}\d+`, `prefix-([a-z]?)-.*`} {
		value, err := randomBySimpleRegex(pattern)
		if err != nil {
			t.Fatalf("%s: %v", pattern, err)
		}
		if !regexp.MustCompile(`^(?:` + pattern + `)$`).MatchString(value) {
			t.Fatalf("%q does not match %s", value, pattern)
		}
	}
	for _, pattern := range []string{`[a-z]{100}`, `(?=a)`, `[`} {
		if _, err := randomBySimpleRegex(pattern); err == nil {
			t.Fatalf("invalid/unbounded pattern %q accepted", pattern)
		}
	}
}
