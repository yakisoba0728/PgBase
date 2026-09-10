//go:build integration

package relation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestHydrateFieldsBoundedChargesOnlySelectedMultiRelations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	records := map[uuid.UUID]map[string]any{fixture.sourceOne: {"id": fixture.sourceOne.String()}}
	counter := &countingRowsQuerier{delegate: fixture.conn}
	used, err := fixture.bindings.HydrateFieldsBounded(ctx, counter,
		[]uuid.UUID{fixture.sourceOne}, records, []string{"groups"}, 1)
	if err != nil || used != 1 || counter.queries != 1 || counter.closes != 1 {
		t.Fatalf("selected hydration used/error/queries/closes = %d/%v/%d/%d, want 1/nil/1/1",
			used, err, counter.queries, counter.closes)
	}
	if _, hydrated := records[fixture.sourceOne]["labels"]; hydrated {
		t.Fatalf("unselected labels were hydrated: %#v", records[fixture.sourceOne]["labels"])
	}
	if got := records[fixture.sourceOne]["groups"]; !reflect.DeepEqual(got, []string{fixture.targetThree.String()}) {
		t.Fatalf("selected groups = %#v", got)
	}

	emptyCounter := &countingRowsQuerier{delegate: fixture.conn}
	used, err = fixture.bindings.HydrateFieldsBounded(ctx, emptyCounter,
		[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, nil, 0)
	if err != nil || used != 0 || emptyCounter.queries != 0 {
		t.Fatalf("empty projection used/error/queries = %d/%v/%d", used, err, emptyCounter.queries)
	}

	for _, names := range [][]string{{"missing"}, {"labels", "labels"}} {
		invalidCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateFieldsBounded(ctx, invalidCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, names, 10)
		if used != 0 || !errors.Is(err, ErrInvalidBinding) || invalidCounter.queries != 0 {
			t.Fatalf("invalid projection %v used/error/queries = %d/%v/%d", names, used, err, invalidCounter.queries)
		}
	}

	source := fixture.source
	source.Fields = append([]schema.Field(nil), fixture.source.Fields...)
	source.Fields = append(source.Fields, schema.Field{
		ID: uuid.MustParse("0199aa21-0000-7000-8000-000000000013"), Name: "owner", ColumnName: "owner",
		Type: schema.TypeRelation, Indexed: true, Ordinal: len(source.Fields),
		Relation: relationSpec(fixture.target.ID, schema.RelationSingle, 0, 1,
			schema.RelationStorageInlineFKV1, "owner"),
	})
	catalog := fixture.catalog
	catalog.Collections = map[string]schema.Collection{source.Name: source, fixture.target.Name: fixture.target}
	withSingle, err := Bind(catalog, source)
	if err != nil {
		t.Fatal(err)
	}
	singleCounter := &countingRowsQuerier{delegate: fixture.conn}
	used, err = withSingle.HydrateFieldsBounded(ctx, singleCounter,
		[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, []string{"owner"}, 10)
	if used != 0 || !errors.Is(err, ErrInvalidBinding) || singleCounter.queries != 0 {
		t.Fatalf("single selector used/error/queries = %d/%v/%d", used, err, singleCounter.queries)
	}

	legacyCounter := &countingRowsQuerier{delegate: fixture.conn}
	legacyRecords := map[uuid.UUID]map[string]any{fixture.sourceOne: {}}
	used, err = withSingle.HydrateBounded(ctx, legacyCounter,
		[]uuid.UUID{fixture.sourceOne}, legacyRecords, 2)
	if err != nil || used != 2 || legacyCounter.queries != 1 ||
		!reflect.DeepEqual(legacyRecords[fixture.sourceOne]["labels"], []string{fixture.targetOne.String()}) ||
		!reflect.DeepEqual(legacyRecords[fixture.sourceOne]["groups"], []string{fixture.targetThree.String()}) {
		t.Fatalf("legacy all-field hydration with single binding used/error/queries/record = %d/%v/%d/%#v",
			used, err, legacyCounter.queries, legacyRecords[fixture.sourceOne])
	}
}
