package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestApplyMigrationSnapshotRequiresRuleValidator(t *testing.T) {
	err := ApplyMigrationSnapshot(context.Background(), nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rule validator is required") {
		t.Fatalf("nil migration rule validator error=%v", err)
	}
}

func TestCanonicalMigrationSnapshotRejectsTrailingJSONAndSortsCollections(t *testing.T) {
	firstID := uuid.MustParse("0199ab10-0000-7000-8000-000000000001")
	secondID := uuid.MustParse("0199ab10-0000-7000-8000-000000000002")
	snapshot := MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{
		{Name: "zeta", Fields: []FieldInput{{ID: &secondID, Name: "value", Type: TypeText}}, Rules: json.RawMessage(`{"z":1,"a":2}`)},
		{Name: "alpha", Fields: []FieldInput{{ID: &firstID, Name: "value", Type: TypeText}}, Rules: json.RawMessage(`{"view":{"op":"allow"}}`)},
	}}
	one, err := MarshalMigrationSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	two, err := CanonicalMigrationSnapshot(one)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) || bytes.Index(one, []byte(`"alpha"`)) > bytes.Index(one, []byte(`"zeta"`)) {
		t.Fatalf("snapshot is not canonical:\n%s", one)
	}
	if _, err := CanonicalMigrationSnapshot(append(one, []byte(` {}`)...)); err == nil {
		t.Fatal("accepted trailing JSON value")
	}
}

func TestCanonicalMigrationSnapshotRejectsTrailingNestedJSON(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000003")
	for _, test := range []struct {
		name     string
		default_ json.RawMessage
		rules    json.RawMessage
	}{
		{name: "field default", default_: json.RawMessage(`1 2`), rules: json.RawMessage(`{}`)},
		{name: "collection rules", rules: json.RawMessage(`{} {}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := MarshalMigrationSnapshot(MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{{
				Name: "items", Fields: []FieldInput{{ID: &fieldID, Name: "value", Type: TypeInteger, Default: test.default_}}, Rules: test.rules,
			}}})
			if err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
				t.Fatalf("trailing nested JSON error=%v", err)
			}
		})
	}
}

func TestCanonicalMigrationSnapshotSortsSetLikeFieldOptions(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000004")
	leftOptions := &FieldOptions{
		AllowedDomains: []string{"z.example", "a.example", "z.example"},
		AllowedValues:  []string{"z", "a", "z"},
		MimeTypes:      []string{"image/png", "image/*", "image/png"},
		Thumbs:         []string{"320x240", "100x100", "320x240"},
		Pattern:        "^[a-z]+$",
	}
	rightOptions := &FieldOptions{
		AllowedDomains: []string{"a.example", "z.example"},
		AllowedValues:  []string{"a", "z"},
		MimeTypes:      []string{"image/*", "image/png"},
		Thumbs:         []string{"100x100", "320x240"},
		Pattern:        "^[a-z]+$",
	}
	makeSnapshot := func(options *FieldOptions) []byte {
		return mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{{
			Name: "items", Fields: []FieldInput{{ID: &fieldID, Name: "value", Type: TypeText, Options: options}}, Rules: json.RawMessage(`{}`),
		}}})
	}
	if left, right := makeSnapshot(leftOptions), makeSnapshot(rightOptions); !bytes.Equal(left, right) {
		t.Fatalf("set-like option permutations differ:\n%s\n%s", left, right)
	}

	// This local shape proves the JSON-name seam used when N4's FieldOptions
	// members are merged into this branch.
	n4 := &struct {
		AutogeneratePattern string   `json:"autogeneratePattern,omitempty"`
		DeniedDomains       []string `json:"deniedDomains,omitempty"`
	}{AutogeneratePattern: "acct-{n}", DeniedDomains: []string{"z.example", "a.example", "z.example"}}
	canonicalizeSnapshotStringSets(n4)
	if got := strings.Join(n4.DeniedDomains, ","); got != "a.example,z.example" || n4.AutogeneratePattern != "acct-{n}" {
		t.Fatalf("N4 option canonicalization=%+v", n4)
	}
}

func TestAnalyzeMigrationSnapshotsReportsEachCollectionUpdate(t *testing.T) {
	one := uuid.MustParse("0199ab10-0000-7000-8000-000000000011")
	two := uuid.MustParse("0199ab10-0000-7000-8000-000000000012")
	before := mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{
		{Name: "alpha", Fields: []FieldInput{{ID: &one, Name: "old", Type: TypeText}}, Rules: json.RawMessage(`{}`)},
		{Name: "beta", Fields: []FieldInput{{ID: &two, Name: "value", Type: TypeText}}, Rules: json.RawMessage(`{}`)},
	}})
	after := mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{
		{Name: "alpha", Fields: []FieldInput{{ID: &one, Name: "new", Type: TypeText}}, Rules: json.RawMessage(`{}`)},
		{Name: "beta", Fields: []FieldInput{{ID: &two, Name: "value", Type: TypeText, Options: &FieldOptions{Pattern: "^[a-z]+$"}}}, Rules: json.RawMessage(`{}`)},
	}})
	impact, err := AnalyzeMigrationSnapshots(before, after)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(impact.Operations, "\n")
	for _, expected := range []string{"rename field alpha.old to new", "update collection beta"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("operations=%v missing %q", impact.Operations, expected)
		}
	}
}

func TestMigrationSnapshotMatchesInheritsOnlyAbsentLegacyMetadata(t *testing.T) {
	fieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000021")
	actualFieldID := uuid.MustParse("0199ab10-0000-7000-8000-000000000022")
	compatibilityName, collectionType, system, validationMode, fieldSystem := "Items", CollectionTypeBase, true, ValidationModePocketBase, true
	actual := mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{{
		Name: "items", CompatibilityName: &compatibilityName, Type: &collectionType, System: &system, ValidationMode: &validationMode,
		Fields: []FieldInput{{ID: &actualFieldID, Name: "created", Type: TypeTimestampTZ, System: &fieldSystem}}, Rules: json.RawMessage(`{}`),
	}}})
	legacy := mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{{
		Name: "items", Fields: []FieldInput{{ID: &fieldID, Name: "created", Type: TypeTimestampTZ}}, Rules: json.RawMessage(`{}`),
	}}})
	if matches, err := migrationSnapshotMatches(actual, legacy); err != nil || !matches {
		t.Fatalf("legacy snapshot match=(%v, %v), want true", matches, err)
	}

	explicitPostgres := ValidationModePostgres
	requested := mustMigrationSnapshot(t, MigrationSnapshot{Format: MigrationSnapshotFormat, Collections: []MigrationSnapshotCollection{{
		Name: "items", ValidationMode: &explicitPostgres,
		Fields: []FieldInput{{ID: &fieldID, Name: "created", Type: TypeTimestampTZ}}, Rules: json.RawMessage(`{}`),
	}}})
	if matches, err := migrationSnapshotMatches(actual, requested); err != nil || matches {
		t.Fatalf("explicit metadata mismatch=(%v, %v), want false", matches, err)
	}
}

func TestMigrationSnapshotIndexPreservesLogicalDefinition(t *testing.T) {
	index := Index{
		Name: "search_items", Fields: []string{"title", "score"}, Unique: true,
		Keys:  []IndexKeyInput{{Expression: "lower(title)", Collation: "C", Order: "DESC"}},
		Where: "score > 0",
	}
	got := migrationSnapshotIndex(index)
	if !reflect.DeepEqual(got.Keys, index.Keys) || got.Where != index.Where || len(got.Fields) != 0 || !got.Unique {
		t.Fatalf("migration snapshot index lost logical definition: %+v", got)
	}
	got.Keys[0].Expression = "changed"
	ordinary := migrationSnapshotIndex(Index{Name: "ordinary", Fields: []string{"title"}})
	ordinary.Fields[0] = "changed"
	if index.Fields[0] != "title" || index.Keys[0].Expression != "lower(title)" {
		t.Fatal("migration snapshot index aliases catalog slices")
	}
}

func mustMigrationSnapshot(t *testing.T, snapshot MigrationSnapshot) []byte {
	t.Helper()
	raw, err := MarshalMigrationSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
