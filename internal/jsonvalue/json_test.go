package jsonvalue

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidRejectsUnicodeAndPostgreSQLNumericValuesThatWouldBeReinterpretedOrRejected(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "invalid UTF-8", raw: []byte{'"', 0xff, '"'}},
		{name: "NUL escape", raw: []byte(`"before\u0000after"`)},
		{name: "lone high surrogate", raw: []byte(`"\ud800"`)},
		{name: "lone low surrogate", raw: []byte(`"\udc00"`)},
		{name: "surrogate with non-low pair", raw: []byte(`"\ud800\u0041"`)},
		{name: "too many integer digits", raw: []byte(`1e131072`)},
		{name: "too many fraction digits", raw: []byte(`1e-16384`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if Valid(test.raw) {
				t.Fatalf("Valid(%q) = true", test.raw)
			}
		})
	}
}

func TestCanonicalPreservesExactNumbersAndValidUnicode(t *testing.T) {
	raw := []byte(` {"large":9007199254740993,"pair":"\ud83d\ude00","fraction":1e-16383} `)
	canonical, err := Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{"9007199254740993", "1e-16383"} {
		if !bytes.Contains(canonical, []byte(exact)) {
			t.Fatalf("canonical JSON lost %s: %s", exact, canonical)
		}
	}
	if !strings.Contains(string(canonical), "😀") {
		t.Fatalf("canonical JSON did not decode valid surrogate pair: %s", canonical)
	}
}

func TestValidPostgresNumberHonorsBoundaryDigits(t *testing.T) {
	if !ValidPostgresNumber("1e131071") || ValidPostgresNumber("1e131072") {
		t.Fatal("integer digit boundary is incorrect")
	}
	if !ValidPostgresNumber("1e-16383") || ValidPostgresNumber("1e-16384") {
		t.Fatal("fraction digit boundary is incorrect")
	}
}
