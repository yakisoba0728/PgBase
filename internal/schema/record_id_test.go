package schema

import "testing"

func TestRecordIDPolicyDefaultsAndExactValidation(t *testing.T) {
	policy, err := NormalizeRecordIDPolicy(&RecordIDPolicy{Kind: RecordIDText})
	if err != nil {
		t.Fatal(err)
	}
	if policy != DefaultTextRecordIDPolicy() {
		t.Fatalf("text defaults = %#v", policy)
	}
	for _, value := range []string{"abc123def456ghi", "ABC123def456ghi", "abc/123def456gh", "CON"} {
		code := ValidateRecordIDValue(policy, value)
		if value == "abc123def456ghi" && code != "" {
			t.Fatalf("valid default ID rejected: %s", code)
		}
		if value != "abc123def456ghi" && code == "" {
			t.Fatalf("invalid default ID accepted: %q", value)
		}
	}

	custom, err := NormalizeRecordIDPolicy(&RecordIDPolicy{Kind: RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`})
	if err != nil {
		t.Fatal(err)
	}
	if code := ValidateRecordIDValue(custom, "CaseSensitive01"); code != "" {
		t.Fatalf("custom exact ID rejected: %s", code)
	}
}

func TestRecordIDPolicyRejectsUnsafeOrUnboundedDefinitions(t *testing.T) {
	for _, policy := range []*RecordIDPolicy{
		{Kind: RecordIDUUID, Pattern: `.*`},
		{Kind: RecordIDText, MinLength: 0, MaxLength: 256, Pattern: `.*`},
		{Kind: RecordIDText, MinLength: 1, MaxLength: 15},
		{Kind: RecordIDText, MinLength: 1, MaxLength: 15, Pattern: `.*`, AutogeneratePattern: `.*`},
	} {
		if _, err := NormalizeRecordIDPolicy(policy); err == nil {
			t.Fatalf("unsafe policy accepted: %#v", policy)
		}
	}
}
