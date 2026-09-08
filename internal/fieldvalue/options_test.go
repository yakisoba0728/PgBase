package fieldvalue

import "testing"

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
