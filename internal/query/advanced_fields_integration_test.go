//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestGeoPointFilterSortAndCursorAgainstPostgres(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "geo_cities", Fields: []schema.FieldInput{{Name: "center", Type: schema.TypeGeoPoint, Indexed: true}}})
	cityWest, err := records.Create(ctx, "geo_cities", map[string]json.RawMessage{"center": json.RawMessage(`{"lon":-40,"lat":20}`)})
	if err != nil {
		t.Fatal(err)
	}
	cityEast, err := records.Create(ctx, "geo_cities", map[string]json.RawMessage{"center": json.RawMessage(`{"lon":40,"lat":-20}`)})
	if err != nil {
		t.Fatal(err)
	}
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "geo_records", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText},
		{Name: "location", Type: schema.TypeGeoPoint, Indexed: true},
		{Name: "city", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "geo_cities", Cardinality: schema.RelationSingle, MaxSelect: intPointerForQueryTest(1), OnDelete: schema.RelationRestrict}},
	}})
	ids := map[string]string{}
	for name, value := range map[string][2]string{"west": {`{"lon":-20,"lat":5}`, cityWest["id"].(string)}, "middle": {`{"lon":0,"lat":10}`, cityEast["id"].(string)}, "east": {`{"lon":30,"lat":-5}`, cityEast["id"].(string)}} {
		record, err := records.Create(ctx, "geo_records", map[string]json.RawMessage{"name": json.RawMessage(`"` + name + `"`), "location": json.RawMessage(value[0]), "city": json.RawMessage(`"` + value[1] + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = record["id"].(string)
	}
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}
	filtered, err := records.ListRelationAware(ctx, RelationListRequest{Principal: principal, Collection: "geo_records", Filter: "location.lat > 0", Sort: "location.lon", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := recordIDs(filtered.Items); !reflect.DeepEqual(got, []string{ids["west"], ids["middle"]}) {
		t.Fatalf("filtered ids=%v", got)
	}
	related, err := records.ListRelationAware(ctx, RelationListRequest{Principal: principal, Collection: "geo_records", Filter: "city.center.lat > 0", Sort: "city.center.lon", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := recordIDs(related.Items); !reflect.DeepEqual(got, []string{ids["west"]}) {
		t.Fatalf("related ids=%v", got)
	}
	first, err := records.ListRelationAware(ctx, RelationListRequest{Principal: principal, Collection: "geo_records", Sort: "location.lon", Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := records.ListRelationAware(ctx, RelationListRequest{Principal: principal, Collection: "geo_records", Sort: "location.lon", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := recordIDs(second.Items); !reflect.DeepEqual(got, []string{ids["middle"]}) {
		t.Fatalf("second ids=%v", got)
	}
}

func intPointerForQueryTest(value int) *int { return &value }
