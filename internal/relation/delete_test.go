package relation

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestBindDeleteGraphRequiresExactIncomingIndex(t *testing.T) {
	catalog, collection := deleteCatalogFixture()
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		t.Fatalf("bind valid delete graph: %v", err)
	}
	if graph.catalogVersion != catalog.Version || len(graph.incoming[collection.ID]) != len(collection.Fields) {
		t.Fatalf("unexpected bound graph: %#v", graph)
	}

	tests := []struct {
		name   string
		mutate func(schema.Catalog) schema.Catalog
	}{
		{
			name: "missing incoming relation",
			mutate: func(value schema.Catalog) schema.Catalog {
				value.Incoming[collection.ID] = value.Incoming[collection.ID][:len(value.Incoming[collection.ID])-1]
				return value
			},
		},
		{
			name: "duplicate incoming relation",
			mutate: func(value schema.Catalog) schema.Catalog {
				value.Incoming[collection.ID] = append(value.Incoming[collection.ID], value.Incoming[collection.ID][0])
				return value
			},
		},
		{
			name: "tampered incoming action",
			mutate: func(value schema.Catalog) schema.Catalog {
				refs := append([]schema.RelationRef(nil), value.Incoming[collection.ID]...)
				refs[0].Spec.OnDelete = schema.RelationRestrict
				value.Incoming[collection.ID] = refs
				return value
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken, _ := deleteCatalogFixture()
			broken = test.mutate(broken)
			bound, err := BindDeleteGraph(broken)
			if bound != nil || !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("invalid Incoming accepted: graph=%v err=%v", bound, err)
			}
		})
	}
}

func TestDeletePlanViewsAreDeterministicAndOwned(t *testing.T) {
	graph := &DeleteGraph{}
	firstCollection := uuid.MustParse("0199aa22-0000-7000-8000-000000000001")
	secondCollection := uuid.MustParse("0199aa22-0000-7000-8000-000000000002")
	firstRecord := uuid.MustParse("0199aa22-0000-7000-8000-000000000011")
	secondRecord := uuid.MustParse("0199aa22-0000-7000-8000-000000000012")
	plan := DeletePlan{
		owner: graph,
		deletes: []RecordRef{
			{CollectionID: firstCollection, RecordID: firstRecord},
			{CollectionID: secondCollection, RecordID: secondRecord},
		},
		clears: []Clear{
			{Source: RecordRef{CollectionID: secondCollection, RecordID: secondRecord}, FieldName: "zeta", TargetIDs: []uuid.UUID{firstRecord}},
			{Source: RecordRef{CollectionID: secondCollection, RecordID: secondRecord}, FieldName: "alpha", TargetIDs: []uuid.UUID{secondRecord}},
		},
	}

	deletes := plan.Deletes()
	deletes[0].RecordID = uuid.New()
	if reflect.DeepEqual(deletes, plan.Deletes()) {
		t.Fatal("Deletes exposed mutable plan state")
	}
	clears := plan.Clears()
	clears[0].TargetIDs[0] = uuid.New()
	if reflect.DeepEqual(clears, plan.Clears()) {
		t.Fatal("Clears exposed mutable plan state")
	}
	updates := plan.Updates()
	if len(updates) != 1 || !reflect.DeepEqual(updates[0].ChangedFields, []string{"alpha", "zeta"}) {
		t.Fatalf("unexpected grouped updates: %#v", updates)
	}
	updates[0].ChangedFields[0] = "changed"
	if reflect.DeepEqual(updates, plan.Updates()) {
		t.Fatal("Updates exposed mutable plan state")
	}
}

func TestDeleteProductionBudgetsAreHardBounded(t *testing.T) {
	if productionDeleteLimits != (deleteLimits{
		depth: 32, records: 10_000, edges: 100_000,
	}) {
		t.Fatalf("unexpected production delete limits: %#v", productionDeleteLimits)
	}
	invalid := []deleteLimits{
		{depth: 33, records: 1, edges: 1},
		{depth: 1, records: 10_001, edges: 1},
		{depth: 1, records: 1, edges: 100_001},
		{depth: -1, records: 1, edges: 1},
	}
	for _, limits := range invalid {
		if validDeleteLimits(limits) {
			t.Fatalf("accepted invalid limits: %#v", limits)
		}
	}
}

func FuzzSortDeleteRecordRefs(f *testing.F) {
	f.Add([]byte("seed"))
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Fuzz(func(t *testing.T, data []byte) {
		records := make([]RecordRef, 0, len(data)/32)
		for len(data) >= 32 && len(records) < 100 {
			var collectionID, recordID uuid.UUID
			copy(collectionID[:], data[:16])
			copy(recordID[:], data[16:32])
			records = append(records, RecordRef{CollectionID: collectionID, RecordID: recordID})
			data = data[32:]
		}
		sortRecordRefs(records)
		for index := 1; index < len(records); index++ {
			if recordRefLess(records[index], records[index-1]) {
				t.Fatalf("records not sorted at %d: %#v", index, records)
			}
		}
		first := append([]RecordRef{}, records...)
		sortRecordRefs(records)
		if !reflect.DeepEqual(first, records) {
			t.Fatal("record ordering is not idempotent")
		}
	})
}

func deleteCatalogFixture() (schema.Catalog, schema.Collection) {
	collectionID := uuid.MustParse("0199aa22-0000-7000-8000-000000000100")
	fields := []schema.Field{
		deleteMultiField(collectionID, "cascade_links", 0, schema.RelationCascade),
		deleteMultiField(collectionID, "restrict_links", 1, schema.RelationRestrict),
		deleteMultiField(collectionID, "null_links", 2, schema.RelationSetNull),
		deleteSingleField(collectionID, "single_cascade", 3, schema.RelationCascade, false),
		deleteSingleField(collectionID, "single_restrict", 4, schema.RelationRestrict, false),
		deleteSingleField(collectionID, "single_null", 5, schema.RelationSetNull, false),
	}
	collection := schema.Collection{ID: collectionID, Name: "items", TableName: "items", Fields: fields}
	refs := make([]schema.RelationRef, 0, len(fields))
	for _, field := range fields {
		refs = append(refs, schema.RelationRef{
			SourceCollectionID: collectionID,
			SourceFieldID:      field.ID,
			Spec:               *field.Relation,
		})
	}
	return schema.Catalog{
		Version:     7,
		Collections: map[string]schema.Collection{"items": collection},
		Incoming:    map[uuid.UUID][]schema.RelationRef{collectionID: refs},
	}, collection
}

func deleteMultiField(target uuid.UUID, name string, ordinal int, action schema.RelationDeleteAction) schema.Field {
	fieldID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("delete-field:"+name))
	storage := schema.NewRelationPhysicalNames("items", name).MultiTable
	return schema.Field{
		ID: fieldID, Name: name, ColumnName: name, Type: schema.TypeRelation, Indexed: true, Ordinal: ordinal,
		Relation: deleteSpec(target, schema.RelationMultiple, 0, 100, action, schema.RelationStorageEdgeTableV1, storage),
	}
}

func deleteSingleField(
	target uuid.UUID,
	name string,
	ordinal int,
	action schema.RelationDeleteAction,
	required bool,
) schema.Field {
	minimum := 0
	if required {
		minimum = 1
	}
	return schema.Field{
		ID:   uuid.NewSHA1(uuid.NameSpaceOID, []byte("delete-field:"+name)),
		Name: name, ColumnName: name, Type: schema.TypeRelation, Required: required, Indexed: true, Ordinal: ordinal,
		Relation: deleteSpec(target, schema.RelationSingle, minimum, 1, action, schema.RelationStorageInlineFKV1, name),
	}
}

func deleteSpec(
	target uuid.UUID,
	cardinality schema.RelationCardinality,
	minimum, maximum int,
	action schema.RelationDeleteAction,
	kind schema.RelationStorageKind,
	name string,
) *schema.RelationSpec {
	return &schema.RelationSpec{
		TargetCollectionID: target,
		Cardinality:        cardinality,
		MinSelect:          minimum,
		MaxSelect:          maximum,
		OnDelete:           action,
		ValueCodecVersion:  schema.RelationValueCodecVersion,
		Storage:            schema.RelationStorage{Kind: kind, Name: name},
	}
}
