package schema

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var relationIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestRelationPhysicalNamesAreDeterministicAndRoleSeparated(t *testing.T) {
	t.Parallel()

	first := NewRelationPhysicalNames("posts", "authors")
	second := NewRelationPhysicalNames("posts", "authors")
	if first != second {
		t.Fatalf("same input produced different names:\nfirst:  %+v\nsecond: %+v", first, second)
	}

	assertRelationPhysicalNames(t, first)
	seen := make(map[string]string)
	value := reflect.ValueOf(first)
	typeOfValue := value.Type()
	for index := 0; index < value.NumField(); index++ {
		name := value.Field(index).String()
		role := typeOfValue.Field(index).Name
		if previousRole, duplicate := seen[name]; duplicate {
			t.Fatalf("roles %s and %s produced the same identifier %q", previousRole, role, name)
		}
		seen[name] = role
	}
}

func TestRelationPhysicalNamesSeparateCollectionAndFieldInputs(t *testing.T) {
	t.Parallel()

	base := relationPhysicalNameValues(NewRelationPhysicalNames("posts", "authors"))
	otherCollection := relationPhysicalNameValues(NewRelationPhysicalNames("comments", "authors"))
	otherField := relationPhysicalNameValues(NewRelationPhysicalNames("posts", "editors"))

	for index, name := range base {
		if name == otherCollection[index] {
			t.Fatalf("role %d did not separate source collections: %q", index, name)
		}
		if name == otherField[index] {
			t.Fatalf("role %d did not separate fields: %q", index, name)
		}
	}
}

func TestRelationPhysicalNamesHideRawNamesAndUseSeparatePrefix(t *testing.T) {
	t.Parallel()

	const source = "customer_secret_collection"
	const field = "private_owner_field"
	for _, name := range relationPhysicalNameValues(NewRelationPhysicalNames(source, field)) {
		if strings.Contains(name, source) || strings.Contains(name, field) {
			t.Fatalf("identifier exposes a raw user name: %q", name)
		}
		if strings.HasPrefix(name, "idx_") || !strings.HasPrefix(name, "rel_") {
			t.Fatalf("identifier %q overlaps the scalar index namespace", name)
		}
	}
}

func TestRelationPhysicalNamesHandleBoundaryInputs(t *testing.T) {
	t.Parallel()

	inputs := []struct {
		source string
		field  string
	}{
		{source: "a", field: "b"},
		{source: strings.Repeat("z", 48), field: strings.Repeat("x", 48)},
		{source: strings.Repeat("collection", 100), field: strings.Repeat("field", 100)},
		{source: "컬렉션/\x00/😀", field: "관계/\x00/東京"},
		{source: "", field: ""},
	}

	for _, input := range inputs {
		names := NewRelationPhysicalNames(input.source, input.field)
		assertRelationPhysicalNames(t, names)
		if names != NewRelationPhysicalNames(input.source, input.field) {
			t.Fatalf("boundary input is nondeterministic: source=%q field=%q", input.source, input.field)
		}
	}
}

func FuzzRelationNameDeterminismAndLimits(f *testing.F) {
	f.Add("posts", "authors")
	f.Add(strings.Repeat("c", 48), strings.Repeat("f", 48))
	f.Add("컬렉션/\x00/😀", "관계/\x00/東京")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, source, field string) {
		if !utf8.ValidString(source) || !utf8.ValidString(field) {
			t.Skip()
		}
		first := NewRelationPhysicalNames(source, field)
		second := NewRelationPhysicalNames(source, field)
		if first != second {
			t.Fatalf("same fuzz input produced different names")
		}
		assertRelationPhysicalNames(t, first)

		values := relationPhysicalNameValues(first)
		seen := make(map[string]struct{}, len(values))
		for _, name := range values {
			if _, duplicate := seen[name]; duplicate {
				t.Fatalf("different roles produced duplicate identifier %q", name)
			}
			seen[name] = struct{}{}
		}
	})
}

func assertRelationPhysicalNames(t *testing.T, names RelationPhysicalNames) {
	t.Helper()
	for _, name := range relationPhysicalNameValues(names) {
		if len(name) == 0 || len(name) > postgresIdentifierLimit {
			t.Fatalf("identifier length = %d for %q, want 1..%d bytes", len(name), name, postgresIdentifierLimit)
		}
		if !relationIdentifierPattern.MatchString(name) {
			t.Fatalf("identifier %q is not an unquoted lowercase PostgreSQL identifier", name)
		}
	}
}

func relationPhysicalNameValues(names RelationPhysicalNames) []string {
	value := reflect.ValueOf(names)
	result := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		result = append(result, value.Field(index).String())
	}
	return result
}
