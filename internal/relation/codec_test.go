package relation

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var (
	testSourceCollectionID = uuid.MustParse("0199aa20-0000-7000-8000-000000000010")
	testTargetCollectionID = uuid.MustParse("0199aa20-0000-7000-8000-000000000020")
	testOwnerFieldID       = uuid.MustParse("0199aa20-0000-7000-8000-000000000101")
	testParentFieldID      = uuid.MustParse("0199aa20-0000-7000-8000-000000000102")
	testTagsFieldID        = uuid.MustParse("0199aa20-0000-7000-8000-000000000103")
	testLabelsFieldID      = uuid.MustParse("0199aa20-0000-7000-8000-000000000104")
	testAuthorFieldID      = uuid.MustParse("0199aa20-0000-7000-8000-000000000105")
)

func TestDecodePreservesOmittedClearAndCanonicalOrder(t *testing.T) {
	bindings := mustCodecBindings(t)
	first := uuid.MustParse("0199aa20-0000-7000-8000-000000000201")
	second := uuid.MustParse("0199aa20-0000-7000-8000-000000000202")
	scalar := json.RawMessage(" {\"kept\": true} ")
	payload := map[string]json.RawMessage{
		"metadata": scalar,
		"owner":    json.RawMessage("null"),
		"tags":     json.RawMessage(`["` + strings.ToUpper(first.String()) + `","` + second.String() + `"]`),
		"labels":   json.RawMessage(`[]`),
	}

	decoded, err := bindings.Decode(PatchInput, payload)
	if err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if !bytes.Equal(decoded.Scalar["metadata"], scalar) {
		t.Fatalf("scalar JSON changed: got %q want %q", decoded.Scalar["metadata"], scalar)
	}
	scalar[1] = '['
	if bytes.Equal(decoded.Scalar["metadata"], scalar) {
		t.Fatal("decoded scalar aliases request bytes")
	}
	if string(decoded.Scalar["owner"]) != "null" {
		t.Fatalf("single relation was not retained for scalar compiler: %q", decoded.Scalar["owner"])
	}
	if _, exists := decoded.Scalar["tags"]; exists {
		t.Fatal("multi relation leaked into scalar input")
	}

	values := relationValuesByName(decoded)
	if !values["owner"].Present || !values["owner"].Clear || string(values["owner"].JSON) != "null" {
		t.Fatalf("single clear was not preserved: %#v", values["owner"])
	}
	if values["parent"].Present || values["parent"].Clear || values["parent"].JSON != nil {
		t.Fatalf("omitted self relation was not preserved: %#v", values["parent"])
	}
	if !reflect.DeepEqual(values["tags"].IDs, []uuid.UUID{first, second}) {
		t.Fatalf("multi order changed: %v", values["tags"].IDs)
	}
	if string(values["tags"].JSON) != `["`+first.String()+`","`+second.String()+`"]` {
		t.Fatalf("multi UUIDs were not canonicalized: %s", values["tags"].JSON)
	}
	if !values["labels"].Present || !values["labels"].Clear || string(values["labels"].JSON) != "[]" {
		t.Fatalf("explicit multi clear was not preserved: %#v", values["labels"])
	}

	returned := decoded.Relations()
	returned[0].IDs = append(returned[0].IDs, uuid.New())
	returned[0].JSON = json.RawMessage(`"changed"`)
	if reflect.DeepEqual(returned, decoded.Relations()) {
		t.Fatal("Relations returned mutable internal state")
	}
}

func TestDecodeCreateAndPatchOmissionHaveDifferentMinimumSemantics(t *testing.T) {
	bindings := mustCodecBindings(t)
	_, err := bindings.Decode(CreateInput, map[string]json.RawMessage{})
	assertValidation(t, err, "tags", "relation_min_select")

	decoded, err := bindings.Decode(PatchInput, map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("decode omitted patch: %v", err)
	}
	for _, value := range decoded.Relations() {
		if value.Present || value.Clear {
			t.Fatalf("patch omission changed field %q: %#v", value.FieldName, value)
		}
	}
}

func TestDecodeStableValidationCodes(t *testing.T) {
	bindings := mustCodecBindings(t)
	first := uuid.MustParse("0199aa20-0000-7000-8000-000000000301")
	second := uuid.MustParse("0199aa20-0000-7000-8000-000000000302")
	third := uuid.MustParse("0199aa20-0000-7000-8000-000000000303")
	tests := []struct {
		name  string
		field string
		raw   string
		code  string
	}{
		{name: "single object", field: "owner", raw: `{}`, code: "relation_invalid_value"},
		{name: "single nil UUID", field: "owner", raw: `"00000000-0000-0000-0000-000000000000"`, code: "relation_invalid_value"},
		{name: "required single clear", field: "author", raw: `null`, code: "relation_min_select"},
		{name: "required multi null", field: "tags", raw: `null`, code: "relation_invalid_value"},
		{name: "multi null", field: "labels", raw: `null`, code: "relation_invalid_value"},
		{name: "multi member type", field: "labels", raw: `[1]`, code: "relation_invalid_value"},
		{name: "duplicate", field: "labels", raw: `["` + first.String() + `","` + strings.ToUpper(first.String()) + `"]`, code: "duplicate_relation_value"},
		{name: "minimum", field: "tags", raw: `[]`, code: "relation_min_select"},
		{name: "maximum", field: "tags", raw: `["` + first.String() + `","` + second.String() + `","` + third.String() + `"]`, code: "relation_max_select"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bindings.Decode(PatchInput, map[string]json.RawMessage{test.field: json.RawMessage(test.raw)})
			assertValidation(t, err, test.field, test.code)
			if strings.Contains(err.Error(), first.String()) || strings.Contains(err.Error(), test.raw) {
				t.Fatalf("validation error disclosed input: %q", err)
			}
		})
	}
}

func TestBindRejectsUntrustedPhysicalStorage(t *testing.T) {
	catalog, source := codecCatalog()
	field := source.Fields[3]
	tampered := *field.Relation
	tampered.Storage.Name = "another_valid_table"
	field.Relation = &tampered
	source.Fields[3] = field
	catalog.Collections[source.Name] = source

	bindings, err := Bind(catalog, source)
	if bindings != nil || !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Bind accepted tampered storage: bindings=%v err=%v", bindings, err)
	}
}

func TestBindRejectsRelationFieldMetadataMismatch(t *testing.T) {
	tests := []struct {
		name   string
		change func(*schema.Collection)
	}{
		{name: "not indexed", change: func(source *schema.Collection) { source.Fields[0].Indexed = false }},
		{name: "required mismatch", change: func(source *schema.Collection) { source.Fields[0].Required = true }},
		{name: "multi unique", change: func(source *schema.Collection) { source.Fields[3].Unique = true }},
		{name: "relation default", change: func(source *schema.Collection) { source.Fields[0].Default = json.RawMessage(`null`) }},
		{name: "virtual column mismatch", change: func(source *schema.Collection) { source.Fields[3].ColumnName = "other" }},
		{name: "ordinal mismatch", change: func(source *schema.Collection) { source.Fields[0].Ordinal = 9 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			catalog, source := codecCatalog()
			test.change(&source)
			catalog.Collections[source.Name] = source
			bindings, err := Bind(catalog, source)
			if bindings != nil || !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("Bind accepted mismatched relation metadata: bindings=%v err=%v", bindings, err)
			}
		})
	}
}

func TestLockOrderDoesNotDependOnRelationValueOrder(t *testing.T) {
	bindings := mustCodecBindings(t)
	first := uuid.MustParse("0199aa20-0000-7000-8000-000000000401")
	second := uuid.MustParse("0199aa20-0000-7000-8000-000000000402")
	source := uuid.MustParse("0199aa20-0000-7000-8000-000000000403")
	forward, err := bindings.Decode(PatchInput, map[string]json.RawMessage{
		"labels": json.RawMessage(`["` + first.String() + `","` + second.String() + `"]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := bindings.Decode(PatchInput, map[string]json.RawMessage{
		"labels": json.RawMessage(`["` + second.String() + `","` + first.String() + `"]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	left := bindings.rowLocks(source, SourceStored, forward)
	right := bindings.rowLocks(source, SourceStored, reverse)
	if len(left) != len(right) {
		t.Fatalf("lock lengths differ: %d != %d", len(left), len(right))
	}
	for index := range left {
		if left[index].collectionID != right[index].collectionID || left[index].recordID != right[index].recordID || left[index].update != right[index].update {
			t.Fatalf("lock order differs at %d: %#v != %#v", index, left[index], right[index])
		}
	}
	for index := 1; index < len(left); index++ {
		previous := [2]uuid.UUID{left[index-1].collectionID, left[index-1].recordID}
		current := [2]uuid.UUID{left[index].collectionID, left[index].recordID}
		if !uuidPairLess(previous, current) {
			t.Fatalf("row locks are not strictly sorted: %v then %v", previous, current)
		}
	}
}

func FuzzDecodeRelationValue(f *testing.F) {
	bindings := mustCodecBindings(f)
	first := uuid.MustParse("0199aa20-0000-7000-8000-000000000501")
	f.Add(`[]`)
	f.Add(`["` + first.String() + `"]`)
	f.Add(`null`)
	f.Add(`{"unexpected":true}`)
	f.Fuzz(func(t *testing.T, raw string) {
		decoded, err := bindings.Decode(PatchInput, map[string]json.RawMessage{"labels": json.RawMessage(raw)})
		if err != nil {
			return
		}
		value := relationValuesByName(decoded)["labels"]
		roundTrip, err := bindings.Decode(PatchInput, map[string]json.RawMessage{"labels": value.JSON})
		if err != nil {
			t.Fatalf("canonical value did not round trip: raw=%q canonical=%q err=%v", raw, value.JSON, err)
		}
		got := relationValuesByName(roundTrip)["labels"]
		if !reflect.DeepEqual(got.IDs, value.IDs) || got.Clear != value.Clear || !bytes.Equal(got.JSON, value.JSON) {
			t.Fatalf("round trip changed value: before=%#v after=%#v", value, got)
		}
	})
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

func mustCodecBindings(t testingTB) *Bindings {
	t.Helper()
	catalog, source := codecCatalog()
	bindings, err := Bind(catalog, source)
	if err != nil {
		t.Fatalf("bind codec fixture: %v", err)
	}
	return bindings
}

func codecCatalog() (schema.Catalog, schema.Collection) {
	tagsTable := schema.NewRelationPhysicalNames("articles", "tags").MultiTable
	labelsTable := schema.NewRelationPhysicalNames("articles", "labels").MultiTable
	source := schema.Collection{
		ID: testSourceCollectionID, Name: "articles", TableName: "articles",
		Fields: []schema.Field{
			{ID: testOwnerFieldID, Name: "owner", ColumnName: "owner", Type: schema.TypeRelation, Indexed: true, Ordinal: 0,
				Relation: relationSpec(testTargetCollectionID, schema.RelationSingle, 0, 1, schema.RelationStorageInlineFKV1, "owner")},
			{ID: testParentFieldID, Name: "parent", ColumnName: "parent", Type: schema.TypeRelation, Indexed: true, Ordinal: 1,
				Relation: relationSpec(testSourceCollectionID, schema.RelationSingle, 0, 1, schema.RelationStorageInlineFKV1, "parent")},
			{ID: testTagsFieldID, Name: "tags", ColumnName: "tags", Type: schema.TypeRelation, Required: true, Indexed: true, Ordinal: 2,
				Relation: relationSpec(testTargetCollectionID, schema.RelationMultiple, 1, 2, schema.RelationStorageEdgeTableV1, tagsTable)},
			{ID: testLabelsFieldID, Name: "labels", ColumnName: "labels", Type: schema.TypeRelation, Indexed: true, Ordinal: 3,
				Relation: relationSpec(testTargetCollectionID, schema.RelationMultiple, 0, 3, schema.RelationStorageEdgeTableV1, labelsTable)},
			{ID: testAuthorFieldID, Name: "author", ColumnName: "author", Type: schema.TypeRelation, Required: true, Indexed: true, Ordinal: 4,
				Relation: relationSpec(testTargetCollectionID, schema.RelationSingle, 1, 1, schema.RelationStorageInlineFKV1, "author")},
		},
	}
	target := schema.Collection{ID: testTargetCollectionID, Name: "people", TableName: "people", Fields: []schema.Field{}}
	return schema.Catalog{
		Version: 1,
		Collections: map[string]schema.Collection{
			source.Name: source,
			target.Name: target,
		},
	}, source
}

func relationSpec(
	target uuid.UUID,
	cardinality schema.RelationCardinality,
	minimum, maximum int,
	kind schema.RelationStorageKind,
	name string,
) *schema.RelationSpec {
	return &schema.RelationSpec{
		TargetCollectionID: target,
		Cardinality:        cardinality,
		MinSelect:          minimum,
		MaxSelect:          maximum,
		OnDelete:           schema.RelationRestrict,
		ValueCodecVersion:  schema.RelationValueCodecVersion,
		Storage:            schema.RelationStorage{Kind: kind, Name: name},
	}
}

func relationValuesByName(input Input) map[string]Value {
	values := make(map[string]Value)
	for _, value := range input.Relations() {
		values[value.FieldName] = value
	}
	return values
}

func assertValidation(t *testing.T, err error, field, code string) {
	t.Helper()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(validationError.Details) != 1 || validationError.Details[0] != (ValidationDetail{Field: field, Code: code}) {
		t.Fatalf("unexpected validation details: %#v", validationError.Details)
	}
}

func uuidPairLess(left, right [2]uuid.UUID) bool {
	leftCollection := left[0].String()
	rightCollection := right[0].String()
	if leftCollection != rightCollection {
		return leftCollection < rightCollection
	}
	return left[1].String() < right[1].String()
}
