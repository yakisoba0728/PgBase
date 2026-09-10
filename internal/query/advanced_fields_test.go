package query

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestGeoPointCodecBoundaries(t *testing.T) {
	field := schema.Field{Name: "location", Type: schema.TypeGeoPoint, Required: true}
	for _, raw := range []string{`{"lon":180,"lat":90}`, `{"lat":-90,"lon":-180}`, `{"lon":0,"lat":1}`} {
		if _, err := decodeFieldValue(field, json.RawMessage(raw)); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"lon":0,"lat":0}`, `{"lon":181,"lat":0}`, `{"lon":0,"lat":91}`, `{"lon":1}`, `{"lon":1,"lat":2,"alt":3}`, `{"lon":"1","lat":2}`} {
		if _, err := decodeFieldValue(field, json.RawMessage(raw)); err == nil {
			t.Fatalf("decode %s succeeded", raw)
		}
	}
}

func TestEditorCodecUsesByteLimitAndNonemptyRequired(t *testing.T) {
	maximum := int64(4)
	field := schema.Field{Name: "body", Type: schema.TypeEditor, Required: true, Options: schema.FieldOptions{MaxSize: &maximum}}
	if _, err := decodeFieldValue(field, json.RawMessage(`""`)); err == nil {
		t.Fatal("empty required editor accepted")
	}
	if _, err := decodeFieldValue(field, json.RawMessage(`"éé"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldValue(field, json.RawMessage(`"ééa"`)); err == nil {
		t.Fatal("five-byte editor accepted")
	}
}

func TestGeoPointDirectAndRelationPathsCompileNumericExpressions(t *testing.T) {
	targetID := uuid.New()
	root := schema.Collection{ID: uuid.New(), Name: "places", TableName: "places", Fields: []schema.Field{{ID: uuid.New(), Name: "location", ColumnName: "location", Type: schema.TypeGeoPoint, Indexed: true, Ordinal: 0}}}
	target := schema.Collection{ID: targetID, Name: "cities", TableName: "cities", Fields: []schema.Field{{ID: uuid.New(), Name: "center", ColumnName: "center", Type: schema.TypeGeoPoint, Indexed: true, Ordinal: 0}}}
	relation := schema.Field{ID: uuid.New(), Name: "city", ColumnName: "city", Type: schema.TypeRelation, Indexed: true, Ordinal: 1, Relation: &schema.RelationSpec{TargetCollectionID: targetID, Cardinality: schema.RelationSingle, MaxSelect: 1, OnDelete: schema.RelationRestrict, ValueCodecVersion: schema.RelationValueCodecVersion, Storage: schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1, Name: "city"}}}
	root.Fields = append(root.Fields, relation)
	catalog := schema.Catalog{Collections: map[string]schema.Collection{"places": root, "cities": target}}
	catalog.Incoming = map[uuid.UUID][]schema.RelationRef{}
	for _, input := range []string{"location.lat > 1", "city.center.lon < 2"} {
		node, err := parseFilter(input)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := compileRelationListPlan(catalog, root, node, []SortKey{{Field: "id"}})
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		statement, err := plan.pageStatementWithPolicy(nil, 10, nil, ruleBinding{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement.SQL, "->>") || !strings.Contains(statement.SQL, "::numeric") {
			t.Fatalf("sql=%s", statement.SQL)
		}
	}
	for _, field := range []string{"location.lon", "city.center.lat"} {
		plan, err := compileRelationListPlan(catalog, root, nil, []SortKey{{Field: field}, {Field: "id"}})
		if err != nil {
			t.Fatalf("sort %s: %v", field, err)
		}
		statement, err := plan.pageStatementWithPolicy(nil, 10, nil, ruleBinding{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement.SQL, "->>") || !strings.Contains(statement.SQL, "::numeric") {
			t.Fatalf("sort sql=%s", statement.SQL)
		}
	}
	if _, err := resolveRelationPath(catalog, root, "location.alt", "filter"); err == nil {
		t.Fatal("unknown coordinate accepted")
	}
}
