package schema

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	testSourceCollectionID = uuid.MustParse("0199a954-8ef2-7000-8000-000000000001")
	testTargetCollectionID = uuid.MustParse("0199a954-8ef2-7000-8000-000000000002")
	testSourceFieldID      = uuid.MustParse("0199a954-8ef2-7000-8000-000000000003")
)

func TestNormalizeRelationFieldAppliesSingleDefaultsWithoutAliasing(t *testing.T) {
	targetID := testTargetCollectionID
	input := FieldInput{
		Name:     "owner",
		Type:     TypeRelation,
		Default:  nil,
		Relation: &RelationInput{TargetCollection: "users"},
	}

	normalized, planned, err := NormalizeRelationField("fields.0", "posts", input, &targetID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if planned == nil {
		t.Fatal("planned relation is nil")
	}
	if got, want := normalized.Indexed, true; got != want {
		t.Errorf("normalized Indexed = %t, want %t", got, want)
	}
	if got, want := *normalized.Relation, (RelationInput{
		TargetCollection: "users",
		Cardinality:      RelationSingle,
		MinSelect:        intPointer(0),
		MaxSelect:        intPointer(1),
		OnDelete:         RelationRestrict,
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("normalized relation = %#v, want %#v", got, want)
	}
	if got, want := *planned, (PlannedRelation{
		TargetCollection:   "users",
		TargetCollectionID: &testTargetCollectionID,
		Cardinality:        RelationSingle,
		MinSelect:          0,
		MaxSelect:          1,
		OnDelete:           RelationRestrict,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage:            RelationStorage{Kind: RelationStorageInlineFKV1, Name: "owner"},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("planned relation = %#v, want %#v", got, want)
	}

	if input.Relation.Cardinality != "" || input.Relation.MinSelect != nil || input.Relation.MaxSelect != nil || input.Indexed {
		t.Fatalf("normalization mutated input: %#v", input)
	}
	if normalized.Relation == input.Relation || planned.TargetCollectionID == &targetID {
		t.Fatal("normalization retained caller-owned relation pointers")
	}
	targetID = uuid.Nil
	if *planned.TargetCollectionID != testTargetCollectionID {
		t.Errorf("planned target changed through caller alias: %s", *planned.TargetCollectionID)
	}
	*normalized.Relation.MinSelect = 1
	if planned.MinSelect != 0 {
		t.Errorf("planned min changed through normalized input alias: %d", planned.MinSelect)
	}
}

func TestNormalizeRelationFieldRequiredAndMultipleDefaults(t *testing.T) {
	tests := []struct {
		name        string
		field       FieldInput
		storageName string
		wantMin     int
		wantMax     int
		wantKind    RelationStorageKind
	}{
		{
			name: "required single",
			field: FieldInput{
				Name: "owner", Type: TypeRelation, Required: true,
				Relation: &RelationInput{TargetCollection: "users"},
			},
			storageName: "owner",
			wantMin:     1,
			wantMax:     1,
			wantKind:    RelationStorageInlineFKV1,
		},
		{
			name: "optional multiple",
			field: FieldInput{
				Name: "tags", Type: TypeRelation,
				Relation: &RelationInput{TargetCollection: "tags", Cardinality: RelationMultiple, MaxSelect: intPointer(20)},
			},
			storageName: "r_1234",
			wantMin:     0,
			wantMax:     20,
			wantKind:    RelationStorageEdgeTableV1,
		},
		{
			name: "required multiple",
			field: FieldInput{
				Name: "members", Type: TypeRelation, Required: true,
				Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: intPointer(1000)},
			},
			storageName: "r_abcd",
			wantMin:     1,
			wantMax:     1000,
			wantKind:    RelationStorageEdgeTableV1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetID := testTargetCollectionID
			normalized, planned, err := NormalizeRelationField("fields.0", "posts", test.field, &targetID, test.storageName)
			if err != nil {
				t.Fatal(err)
			}
			if got := *normalized.Relation.MinSelect; got != test.wantMin {
				t.Errorf("normalized min = %d, want %d", got, test.wantMin)
			}
			if got := *normalized.Relation.MaxSelect; got != test.wantMax {
				t.Errorf("normalized max = %d, want %d", got, test.wantMax)
			}
			if planned.MinSelect != test.wantMin || planned.MaxSelect != test.wantMax || planned.Storage.Kind != test.wantKind {
				t.Errorf("planned relation = %#v", planned)
			}
		})
	}
}

func TestNormalizeAndResolveCreateTimeSelfRelation(t *testing.T) {
	input := FieldInput{
		Name: "parent", Type: TypeRelation,
		Relation: &RelationInput{TargetCollection: "nodes", OnDelete: RelationSetNull},
	}
	_, planned, err := NormalizeRelationField("fields.0", "nodes", input, nil, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if planned.TargetCollectionID != nil {
		t.Fatalf("self relation target ID = %s, want symbolic nil", *planned.TargetCollectionID)
	}

	spec, err := ResolvePlannedRelation("fields.0", *planned, testSourceCollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if spec.TargetCollectionID != testSourceCollectionID {
		t.Errorf("resolved target = %s, want source %s", spec.TargetCollectionID, testSourceCollectionID)
	}
	planned.Storage.Name = "changed"
	if spec.Storage.Name != "parent" {
		t.Errorf("resolved spec aliased planned storage: %q", spec.Storage.Name)
	}

	_, err = ResolvePlannedRelation("fields.0", *planned, uuid.Nil)
	assertValidationDetails(t, err, []ValidationDetail{{
		Field: "fields.0.relation.targetCollection",
		Code:  "relation_target_collection_not_found",
	}})

	tampered := *planned
	tampered.Storage.Name = "invalid storage"
	_, err = ResolvePlannedRelation("fields.0", tampered, testSourceCollectionID)
	assertValidationDetails(t, err, []ValidationDetail{{
		Field: "fields.0.relation.storage.name",
		Code:  "relation_storage_invalid",
	}})
}

func TestPlannedRelationFromSpecDoesNotAliasCatalogState(t *testing.T) {
	spec := RelationSpec{
		TargetCollectionID: testTargetCollectionID,
		Cardinality:        RelationMultiple,
		MinSelect:          1,
		MaxSelect:          20,
		OnDelete:           RelationCascade,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage:            RelationStorage{Kind: RelationStorageEdgeTableV1, Name: "r_tags"},
	}
	planned := PlannedRelationFromSpec("tags", spec)
	if planned.TargetCollectionID == nil || *planned.TargetCollectionID != testTargetCollectionID {
		t.Fatalf("planned target = %#v", planned.TargetCollectionID)
	}
	*planned.TargetCollectionID = uuid.Nil
	if spec.TargetCollectionID != testTargetCollectionID {
		t.Errorf("catalog spec target changed through planned pointer: %s", spec.TargetCollectionID)
	}
}

func TestValidateRelationSpecRejectsPersistedMetadataDriftDeterministically(t *testing.T) {
	spec := RelationSpec{
		Cardinality:       RelationMultiple,
		MinSelect:         4,
		MaxSelect:         1,
		OnDelete:          RelationSetNull,
		ValueCodecVersion: 2,
		Storage:           RelationStorage{Kind: RelationStorageInlineFKV1, Name: "Bad-Name"},
	}
	err := ValidateRelationSpec("fields.1", spec)
	assertValidationDetails(t, err, []ValidationDetail{
		{Field: "fields.1.relation.maxSelect", Code: "relation_invalid_bounds"},
		{Field: "fields.1.relation.minSelect", Code: "relation_invalid_bounds"},
		{Field: "fields.1.relation.onDelete", Code: "relation_set_null_requires_optional"},
		{Field: "fields.1.relation.storage.kind", Code: "relation_storage_invalid"},
		{Field: "fields.1.relation.storage.name", Code: "relation_storage_invalid"},
		{Field: "fields.1.relation.targetCollection", Code: "relation_target_collection_not_found"},
		{Field: "fields.1.relation.valueCodecVersion", Code: "relation_value_codec_unsupported"},
	})

	valid := RelationSpec{
		TargetCollectionID: testTargetCollectionID,
		Cardinality:        RelationSingle,
		MinSelect:          0,
		MaxSelect:          1,
		OnDelete:           RelationRestrict,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage:            RelationStorage{Kind: RelationStorageInlineFKV1, Name: "owner"},
	}
	if err := ValidateRelationSpec("fields.1", valid); err != nil {
		t.Fatalf("valid persisted spec: %v", err)
	}
}

func TestNormalizeRelationFieldRejectsInvalidCombinations(t *testing.T) {
	validRelation := func() *RelationInput {
		return &RelationInput{TargetCollection: "users"}
	}
	tests := []struct {
		name        string
		field       FieldInput
		source      string
		targetID    *uuid.UUID
		storageName string
		want        []ValidationDetail
	}{
		{
			name:  "scalar relation config",
			field: FieldInput{Name: "title", Type: TypeText, Relation: validRelation()},
			want:  []ValidationDetail{{Field: "fields.3.relation", Code: "relation_config_forbidden"}},
		},
		{
			name:  "relation config required",
			field: FieldInput{Name: "owner", Type: TypeRelation},
			want:  []ValidationDetail{{Field: "fields.3.relation", Code: "relation_config_required"}},
		},
		{
			name:     "target required",
			field:    FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "  "}},
			targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.targetCollection", Code: "relation_target_required"}},
		},
		{
			name:   "target unresolved",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: validRelation()},
			source: "posts", storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.targetCollection", Code: "relation_target_collection_not_found"}},
		},
		{
			name:   "target zero",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: validRelation()},
			source: "posts", targetID: uuidPointer(uuid.Nil), storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.targetCollection", Code: "relation_target_collection_not_found"}},
		},
		{
			name:   "cardinality",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", Cardinality: "many"}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.cardinality", Code: "relation_invalid_cardinality"}},
		},
		{
			name:   "single min below range",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", MinSelect: intPointer(-1)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "single min above range",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Required: true, Relation: &RelationInput{TargetCollection: "users", MinSelect: intPointer(2)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "single max zero",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", MaxSelect: intPointer(0)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.maxSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "single max above one",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", MaxSelect: intPointer(2)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.maxSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "multiple max required",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.relation.maxSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "multiple max below range",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: intPointer(1)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.relation.maxSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "multiple max above range",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: intPointer(1001)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.relation.maxSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "multiple min below range",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MinSelect: intPointer(-1), MaxSelect: intPointer(5)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "multiple min above max",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Required: true, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MinSelect: intPointer(6), MaxSelect: intPointer(5)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_invalid_bounds"}},
		},
		{
			name:   "required min mismatch",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Required: true, Relation: &RelationInput{TargetCollection: "users", MinSelect: intPointer(0)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_required_mismatch"}},
		},
		{
			name:   "optional min mismatch",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", MinSelect: intPointer(1)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.minSelect", Code: "relation_required_mismatch"}},
		},
		{
			name:   "set null must be optional",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Required: true, Relation: &RelationInput{TargetCollection: "users", OnDelete: RelationSetNull}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.onDelete", Code: "relation_set_null_requires_optional"}},
		},
		{
			name:   "delete action",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users", OnDelete: "erase"}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.relation.onDelete", Code: "relation_delete_action_invalid"}},
		},
		{
			name:   "default",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Default: json.RawMessage(`null`), Relation: validRelation()},
			source: "posts", targetID: &testTargetCollectionID, storageName: "owner",
			want: []ValidationDetail{{Field: "fields.3.default", Code: "relation_default_unsupported"}},
		},
		{
			name:   "unique multiple",
			field:  FieldInput{Name: "tags", Type: TypeRelation, Unique: true, Relation: &RelationInput{TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: intPointer(5)}},
			source: "posts", targetID: &testTargetCollectionID, storageName: "r_tags",
			want: []ValidationDetail{{Field: "fields.3.unique", Code: "relation_unique_requires_single"}},
		},
		{
			name:   "storage empty",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: validRelation()},
			source: "posts", targetID: &testTargetCollectionID,
			want: []ValidationDetail{{Field: "fields.3.relation.storage", Code: "relation_storage_invalid"}},
		},
		{
			name:   "storage identifier",
			field:  FieldInput{Name: "owner", Type: TypeRelation, Relation: validRelation()},
			source: "posts", targetID: &testTargetCollectionID, storageName: "Bad-Name",
			want: []ValidationDetail{{Field: "fields.3.relation.storage", Code: "relation_storage_invalid"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := NormalizeRelationField("fields.3", test.source, test.field, test.targetID, test.storageName)
			assertValidationDetails(t, err, test.want)
		})
	}
}

func TestNormalizeRelationFieldSortsAllValidationDetails(t *testing.T) {
	field := FieldInput{
		Name:    "tags",
		Type:    TypeRelation,
		Unique:  true,
		Default: json.RawMessage(`[]`),
		Relation: &RelationInput{
			TargetCollection: "users",
			Cardinality:      RelationMultiple,
			MinSelect:        intPointer(3),
			MaxSelect:        intPointer(2),
			OnDelete:         RelationSetNull,
		},
	}
	_, _, err := NormalizeRelationField("fields.9", "posts", field, &testTargetCollectionID, "")
	assertValidationDetails(t, err, []ValidationDetail{
		{Field: "fields.9.default", Code: "relation_default_unsupported"},
		{Field: "fields.9.relation.minSelect", Code: "relation_invalid_bounds"},
		{Field: "fields.9.relation.minSelect", Code: "relation_required_mismatch"},
		{Field: "fields.9.relation.onDelete", Code: "relation_set_null_requires_optional"},
		{Field: "fields.9.relation.storage", Code: "relation_storage_invalid"},
		{Field: "fields.9.unique", Code: "relation_unique_requires_single"},
	})
}

func TestNormalizeScalarFieldPreservesJSONAndCopiesDefault(t *testing.T) {
	input := FieldInput{Name: "title", Type: TypeText, Default: json.RawMessage(`"hello"`)}
	normalized, relation, err := NormalizeRelationField("fields.0", "posts", input, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if relation != nil {
		t.Fatalf("scalar relation = %#v, want nil", relation)
	}
	input.Default[1] = 'X'
	if got := string(normalized.Default); got != `"hello"` {
		t.Errorf("normalized scalar default changed through alias: %s", got)
	}

	values := []any{
		FieldInput{Name: "title", Type: TypeText},
		PlannedField{Name: "title", Type: TypeText},
		Field{Name: "title", Type: TypeText},
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "relation") {
			t.Errorf("scalar JSON contains relation member: %s", encoded)
		}
	}
}

func TestCloneCatalogDeepCopiesRelationState(t *testing.T) {
	spec := &RelationSpec{
		TargetCollectionID: testTargetCollectionID,
		Cardinality:        RelationSingle,
		MinSelect:          0,
		MaxSelect:          1,
		OnDelete:           RelationRestrict,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage:            RelationStorage{Kind: RelationStorageInlineFKV1, Name: "owner"},
	}
	source := Catalog{
		Version: 7,
		Collections: map[string]Collection{
			"posts": {
				ID: testSourceCollectionID, Name: "posts", TableName: "posts",
				Fields: []Field{{
					ID: testSourceFieldID, Name: "owner", ColumnName: "owner", Type: TypeRelation,
					Default: json.RawMessage(`null`), Relation: spec,
				}},
			},
		},
		Incoming: map[uuid.UUID][]RelationRef{
			testTargetCollectionID: {{
				SourceCollectionID: testSourceCollectionID,
				SourceFieldID:      testSourceFieldID,
				Spec:               *spec,
			}},
		},
	}

	cloned := cloneCatalog(source)
	sourceCollection := source.Collections["posts"]
	sourceCollection.Fields[0].Default[0] = '['
	sourceCollection.Fields[0].Relation.MinSelect = 1
	source.Collections["posts"] = sourceCollection
	source.Incoming[testTargetCollectionID][0].SourceFieldID = uuid.Nil

	clonedField := cloned.Collections["posts"].Fields[0]
	if got := string(clonedField.Default); got != "null" {
		t.Errorf("cloned default changed through source alias: %q", got)
	}
	if clonedField.Relation.MinSelect != 0 {
		t.Errorf("cloned relation changed through source alias: %#v", clonedField.Relation)
	}
	if cloned.Incoming[testTargetCollectionID][0].SourceFieldID != testSourceFieldID {
		t.Errorf("cloned incoming slice changed through source alias: %#v", cloned.Incoming)
	}
	if clonedField.Relation == sourceCollection.Fields[0].Relation {
		t.Fatal("catalog clone retained relation pointer")
	}

	clonedCollection := cloned.Collections["posts"]
	clonedCollection.Fields[0].Relation.Storage.Name = "changed"
	cloned.Collections["posts"] = clonedCollection
	cloned.Incoming[testTargetCollectionID][0].SourceCollectionID = uuid.Nil
	if source.Collections["posts"].Fields[0].Relation.Storage.Name != "owner" {
		t.Error("source relation changed through clone alias")
	}
	if source.Incoming[testTargetCollectionID][0].SourceCollectionID != testSourceCollectionID {
		t.Error("source incoming slice changed through clone alias")
	}
}

func assertValidationDetails(t *testing.T, err error, want []ValidationDetail) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %#v", want)
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if !reflect.DeepEqual(validationError.Details, want) {
		t.Fatalf("validation details = %#v, want %#v", validationError.Details, want)
	}
}

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }
