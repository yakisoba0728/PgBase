package fieldvalue

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestTextNumericAndSelectionContracts(t *testing.T) {
	min, max := 2, 3
	low, high := "1.5", "10"
	if got := ValidateText("user@sub.example.com", Options{Format: "email", AllowedDomains: []string{"example.com"}}); got != "" {
		t.Fatal(got)
	}
	if got := ValidateText("user@elsewhere.test", Options{Format: "email", AllowedDomains: []string{"example.com"}}); got != "domain" {
		t.Fatal(got)
	}
	if got := ValidateNumeric("1.49", Options{Min: &low, Max: &high}); got != "min" {
		t.Fatal(got)
	}
	selectOptions := Options{AllowedValues: []string{"red", "blue", "green"}, MinSelect: &min, MaxSelect: &max}
	if got := ValidateSelection([]string{"red"}, selectOptions); got != "min_select" {
		t.Fatal(got)
	}
	if got := ValidateSelection([]string{"red", "blue"}, selectOptions); got != "" {
		t.Fatal(got)
	}
	if got := ValidateSelection([]string{"red", "red"}, selectOptions); got != "duplicate_value" {
		t.Fatal(got)
	}
}

func TestDomainNormalizationAndDenyPrecedence(t *testing.T) {
	normalized, err := NormalizeDomains([]string{" Sub.Example.COM. ", "example.com"})
	if err != nil || !reflect.DeepEqual(normalized, []string{"example.com", "sub.example.com"}) {
		t.Fatalf("normalized=%v err=%v", normalized, err)
	}
	if nilDomains, err := NormalizeDomains(nil); err != nil || nilDomains != nil {
		t.Fatalf("nil normalized=%v err=%v", nilDomains, err)
	}
	for _, invalid := range [][]string{{"example.com", "EXAMPLE.COM."}, {"bad_domain"}, {"-bad.test"}, {"bad..test"}} {
		if _, err := NormalizeDomains(invalid); err == nil {
			t.Errorf("accepted domains %v", invalid)
		}
	}
	options := Options{Format: "url", AllowedDomains: []string{"example.com"}, DeniedDomains: []string{"private.example.com"}}
	if got := ValidateText("https://docs.example.com/path", options); got != "" {
		t.Fatalf("allowed URL=%q", got)
	}
	if got := ValidateText("https://x.private.example.com/path", options); got != "domain" {
		t.Fatalf("denied URL=%q", got)
	}
}

func TestRandomStringByPatternIsBoundedAndMatching(t *testing.T) {
	pattern := `item-[a-z0-9]{8}`
	if err := ValidateRandomPattern(pattern, 2048); err != nil {
		t.Fatal(err)
	}
	generated, err := RandomStringByPattern(bytes.NewReader(make([]byte, 128)), pattern)
	if err != nil || !regexp.MustCompile(`^`+pattern+`$`).MatchString(generated) {
		t.Fatalf("generated=%q err=%v", generated, err)
	}
	for _, invalid := range []string{"^anchored$", `[a-z]{1001}`, ``, strings.Repeat("x", 1025)} {
		if ValidateRandomPattern(invalid, 2048) == nil {
			t.Errorf("accepted random pattern %q", invalid)
		}
	}
}

func TestInvalidDefinitionsNeverPanicAtRuntime(t *testing.T) {
	if ValidateDefinition("text", Options{Pattern: "["}) == nil {
		t.Fatal("accepted invalid regex")
	}
	if got := ValidateText("x", Options{Pattern: "["}); got != "pattern" {
		t.Fatal(got)
	}
	huge := "1" + string(make([]byte, 1001))
	if ValidateDefinition("numeric", Options{Min: &huge}) == nil {
		t.Fatal("accepted oversized bound")
	}
}
