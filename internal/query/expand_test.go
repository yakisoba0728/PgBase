package query

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestExpandedRecordFlattenMatchesJSONAndReturnsOwnedCopy(t *testing.T) {
	t.Parallel()
	record := ExpandedRecord{
		Root: map[string]any{
			"id":   "11111111-1111-4111-8111-111111111111",
			"tags": []string{"22222222-2222-4222-8222-222222222222"},
		},
		Expand: map[string]any{
			"owner": map[string]any{"name": "Ada"},
		},
	}
	flat, err := record.Flatten()
	if err != nil {
		t.Fatal(err)
	}
	encodedRecord, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	encodedFlat, err := json.Marshal(flat)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encodedRecord, encodedFlat) {
		t.Fatalf("MarshalJSON = %s, Flatten JSON = %s", encodedRecord, encodedFlat)
	}
	flat["tags"].([]string)[0] = "changed"
	flat["expand"].(map[string]any)["owner"].(map[string]any)["name"] = "changed"
	if record.Root["tags"].([]string)[0] == "changed" || record.Expand["owner"].(map[string]any)["name"] == "changed" {
		t.Fatal("Flatten returned aliases into ExpandedRecord")
	}
}

func TestExpandedRecordFlattenRejectsReservedFieldCollision(t *testing.T) {
	t.Parallel()
	_, err := (ExpandedRecord{
		Root:   map[string]any{"expand": "legacy value"},
		Expand: map[string]any{"owner": map[string]any{"id": "target"}},
	}).Flatten()
	if err == nil {
		t.Fatal("Flatten accepted an expand field collision")
	}
}

func TestValidateExpandedRecordSizeIncludesTrailingNewline(t *testing.T) {
	t.Parallel()
	record := ExpandedRecord{Root: map[string]any{"value": "x"}}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateExpandedRecordSize(record, len(encoded)+1); err != nil {
		t.Fatalf("exact-size record rejected: %v", err)
	}
	err = validateExpandedRecordSize(record, len(encoded))
	assertValidationDetail(t, err, "response", "response_too_large")
}

func TestMaterializedExpansionRejectsBranchingGraphBeforeExponentialFlatten(t *testing.T) {
	t.Parallel()
	leaf := &materializedRecord{root: map[string]any{
		"id":         "11111111-1111-4111-8111-111111111111",
		"created_at": "2026-08-31T00:00:00Z",
		"updated_at": "2026-08-31T00:00:00Z",
	}}
	current := leaf
	for depth := 0; depth < defaultExpandLimits.maxDepth; depth++ {
		children := make([]*materializedRecord, 16)
		for index := range children {
			children[index] = current
		}
		current = &materializedRecord{
			root: map[string]any{
				"id":         uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(depth)}).String(),
				"created_at": "2026-08-31T00:00:00Z",
				"updated_at": "2026-08-31T00:00:00Z",
			},
			expand: map[string]materializedExpandValue{
				"links": {many: children, plural: true},
			},
		}
	}
	measurement := newMaterializedMeasurement(defaultExpandLimits.maxBytes)
	size, err := measurement.recordSize(current)
	if err != nil {
		t.Fatal(err)
	}
	if size <= defaultExpandLimits.maxBytes || len(measurement.cached) != defaultExpandLimits.maxDepth+1 {
		t.Fatalf("bounded measurement size/cache = %d/%d", size, len(measurement.cached))
	}
	usage := &expansionUsage{limits: defaultExpandLimits}
	expanded, err := materializedRecordsToExpanded([]*materializedRecord{current}, usage)
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 1 || !expanded[0].oversized {
		t.Fatalf("branching material graph was flattened: %#v", expanded)
	}
	err = validateExpandedRecordSize(expanded[0], defaultExpandLimits.maxBytes)
	assertValidationDetail(t, err, "response", "response_too_large")
}

func TestMaterializedMeasurementMatchesExactNestedJSONAndBoundsPagePrefix(t *testing.T) {
	t.Parallel()
	child := &materializedRecord{root: map[string]any{"id": "child", "name": "Ada"}}
	root := &materializedRecord{
		root: map[string]any{"id": "root"},
		expand: map[string]materializedExpandValue{
			"owners": {many: []*materializedRecord{child, child}, plural: true},
		},
	}
	measurement := newMaterializedMeasurement(defaultExpandLimits.maxBytes)
	measured, err := measurement.recordSize(root)
	if err != nil {
		t.Fatal(err)
	}
	flattened, err := flattenMaterializedRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(flattened)
	if err != nil {
		t.Fatal(err)
	}
	if measured != len(encoded) {
		t.Fatalf("nested measurement = %d, exact JSON = %d (%s)", measured, len(encoded), encoded)
	}

	large := strings.Repeat("x", defaultExpandLimits.maxBytes/2)
	pageUsage := &expansionUsage{limits: defaultExpandLimits, pageResult: true}
	page, err := materializedRecordsToExpanded([]*materializedRecord{
		{root: map[string]any{"id": "first", "value": large}},
		{root: map[string]any{"id": "second", "value": large}},
	}, pageUsage)
	if err != nil {
		t.Fatal(err)
	}
	if page[0].oversized || !page[1].oversized {
		t.Fatalf("cumulative page bound markers = first:%v second:%v", page[0].oversized, page[1].oversized)
	}
}

func TestCompileExpandPlanValidatesAndBoundsTypedPaths(t *testing.T) {
	t.Parallel()
	rootID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	targetID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	root := schema.Collection{
		ID: rootID, Name: "roots", TableName: "roots",
		Fields: []schema.Field{
			{Name: "title", Type: schema.TypeText, Ordinal: 0},
			{Name: "owner", Type: schema.TypeRelation, Ordinal: 1, Relation: &schema.RelationSpec{
				TargetCollectionID: targetID, Cardinality: schema.RelationSingle,
			}},
		},
	}
	target := schema.Collection{
		ID: targetID, Name: "targets", TableName: "targets",
		Fields: []schema.Field{{Name: "next", Type: schema.TypeRelation, Ordinal: 0, Relation: &schema.RelationSpec{
			TargetCollectionID: targetID, Cardinality: schema.RelationSingle,
		}}},
	}
	catalog := expandPlanTestCatalog(root, target)
	root = catalog.Collections[root.Name]

	plan, err := compileExpandPlan(catalog, root, " owner,owner ", defaultExpandLimits)
	if err != nil || len(plan.nodes) != 1 || plan.nodes[0].field.Name != "owner" {
		t.Fatalf("deduplicated plan = %#v, err=%v", plan, err)
	}
	for _, input := range []string{",owner", "owner,", "title", "missing", "owner..next"} {
		_, err := compileExpandPlan(catalog, root, input, defaultExpandLimits)
		assertValidationDetail(t, err, "expand", "expand_invalid_path")
	}
	nested, err := compileExpandPlan(catalog, root, "owner.next", defaultExpandLimits)
	if err != nil || len(nested.nodes) != 1 || len(nested.nodes[0].children) != 1 || nested.nodes[0].children[0].name != "next" {
		t.Fatalf("nested plan = %#v, err=%v", nested, err)
	}
	_, err = compileExpandPlan(catalog, root, strings.Repeat("owner.", 6)+"owner", defaultExpandLimits)
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")
	_, err = compileExpandPlan(catalog, root, strings.Repeat("x", maximumExpandBytes+1), defaultExpandLimits)
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")
}

func TestCompileExpandPlanBoundsUniqueNodesAndRejectsReservedRootField(t *testing.T) {
	t.Parallel()
	rootID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	targetID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	target := schema.Collection{ID: targetID, Name: "targets", TableName: "targets"}
	root := schema.Collection{ID: rootID, Name: "roots", TableName: "roots"}
	paths := make([]string, 0, defaultExpandLimits.maxNodes+1)
	for index := 0; index <= defaultExpandLimits.maxNodes; index++ {
		name := "relation_" + string(rune('a'+index))
		paths = append(paths, name)
		root.Fields = append(root.Fields, schema.Field{
			Name: name, Type: schema.TypeRelation, Ordinal: index, Relation: &schema.RelationSpec{
				TargetCollectionID: targetID, Cardinality: schema.RelationSingle,
			},
		})
	}
	catalog := expandPlanTestCatalog(root, target)
	root = catalog.Collections[root.Name]
	_, err := compileExpandPlan(catalog, root, strings.Join(paths, ","), defaultExpandLimits)
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")

	root.Fields = append(root.Fields, schema.Field{Name: "expand", Type: schema.TypeText, Ordinal: len(root.Fields)})
	catalog.Collections["roots"] = root
	_, err = compileExpandPlan(catalog, root, paths[0], defaultExpandLimits)
	assertValidationDetail(t, err, "expand", "expand_invalid_path")
}

func TestCompileExpandPlanRejectsForgedIncomingIndex(t *testing.T) {
	t.Parallel()
	targetID := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	root := schema.Collection{
		ID: uuid.MustParse("77777777-7777-4777-8777-777777777777"), Name: "roots", TableName: "roots",
		Fields: []schema.Field{{
			Name: "owner", Type: schema.TypeRelation, Ordinal: 0,
			Relation: &schema.RelationSpec{TargetCollectionID: targetID, Cardinality: schema.RelationSingle},
		}},
	}
	target := schema.Collection{ID: targetID, Name: "targets", TableName: "targets"}
	valid := expandPlanTestCatalog(root, target)
	root = valid.Collections[root.Name]
	if _, err := compileExpandPlan(valid, root, "owner", defaultExpandLimits); err != nil {
		t.Fatalf("valid incoming index rejected: %v", err)
	}

	missing := valid
	missing.Incoming = map[uuid.UUID][]schema.RelationRef{}
	if _, err := compileExpandPlan(missing, root, "owner", defaultExpandLimits); !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("missing incoming error = %v, want schema conflict", err)
	}

	duplicate := valid
	duplicate.Incoming = make(map[uuid.UUID][]schema.RelationRef, len(valid.Incoming))
	for id, refs := range valid.Incoming {
		duplicate.Incoming[id] = append([]schema.RelationRef(nil), refs...)
	}
	duplicate.Incoming[targetID] = append(duplicate.Incoming[targetID], duplicate.Incoming[targetID][0])
	if _, err := compileExpandPlan(duplicate, root, "owner", defaultExpandLimits); !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("duplicate incoming error = %v, want schema conflict", err)
	}

	swappedTable := valid
	swappedTable.Collections = make(map[string]schema.Collection, len(valid.Collections))
	for name, collection := range valid.Collections {
		swappedTable.Collections[name] = collection
	}
	forgedTarget := swappedTable.Collections["targets"]
	forgedTarget.TableName = "roots"
	swappedTable.Collections[forgedTarget.Name] = forgedTarget
	if _, err := compileExpandPlan(swappedTable, root, "owner", defaultExpandLimits); !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("swapped valid table error = %v, want schema conflict", err)
	}
}

func expandPlanTestCatalog(collections ...schema.Collection) schema.Catalog {
	catalog := schema.Catalog{
		Collections: make(map[string]schema.Collection, len(collections)),
		Incoming:    make(map[uuid.UUID][]schema.RelationRef),
	}
	for _, collection := range collections {
		for index := range collection.Fields {
			field := &collection.Fields[index]
			if field.ID == uuid.Nil {
				field.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(collection.Name+"."+field.Name))
			}
			if field.ColumnName == "" {
				field.ColumnName = field.Name
			}
			if field.Type == schema.TypeRelation && field.Relation != nil {
				field.Indexed = true
				if field.Relation.Cardinality == schema.RelationSingle {
					field.Relation.MaxSelect = 1
					field.Relation.Storage = schema.RelationStorage{
						Kind: schema.RelationStorageInlineFKV1, Name: field.Name,
					}
				} else {
					field.Relation.Storage = schema.RelationStorage{
						Kind: schema.RelationStorageEdgeTableV1,
						Name: schema.NewRelationPhysicalNames(collection.TableName, field.Name).MultiTable,
					}
				}
				field.Relation.OnDelete = schema.RelationRestrict
				field.Relation.ValueCodecVersion = schema.RelationValueCodecVersion
			}
		}
		catalog.Collections[collection.Name] = collection
	}
	for _, collection := range catalog.Collections {
		for _, field := range collection.Fields {
			if field.Type != schema.TypeRelation || field.Relation == nil {
				continue
			}
			catalog.Incoming[field.Relation.TargetCollectionID] = append(
				catalog.Incoming[field.Relation.TargetCollectionID],
				schema.RelationRef{
					SourceCollectionID: collection.ID,
					SourceFieldID:      field.ID,
					Spec:               *field.Relation,
				},
			)
		}
	}
	return catalog
}

func TestExpandCatalogLookupFailsClosedForForgedMapKeysAndDuplicateIDs(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	forged := schema.Catalog{Collections: map[string]schema.Collection{
		"constructor": {ID: id, Name: "actual", TableName: "actual"},
	}}
	if _, found := catalogCollectionByID(forged, id); found {
		t.Fatal("catalog lookup trusted a map entry whose key did not match its collection name")
	}
	duplicate := schema.Catalog{Collections: map[string]schema.Collection{
		"first":  {ID: id, Name: "first", TableName: "first"},
		"second": {ID: id, Name: "second", TableName: "second"},
	}}
	if _, found := catalogCollectionByID(duplicate, id); found {
		t.Fatal("catalog lookup accepted duplicate collection IDs")
	}
}

func TestProjectionTypesFailClosed(t *testing.T) {
	t.Parallel()
	collection := schema.Collection{Name: "targets", Fields: []schema.Field{{Name: "name", Type: schema.TypeText}}}
	policy := AdminAllowAll{}
	for _, principal := range []Principal{{}, {Kind: PrincipalAdministrator}, {Kind: "public", ID: uuid.New()}} {
		_, _, err := policy.ReadScope(t.Context(), principal, collection, ReadActionExpand)
		if !errors.Is(err, ErrProjectionDenied) {
			t.Fatalf("principal %#v error = %v, want projection denied", principal, err)
		}
	}
	for _, action := range []ReadAction{ReadActionExpand, ReadActionRelationFilter, ReadActionRelationSort} {
		predicate, fields, err := policy.ReadScope(t.Context(), Principal{
			Kind: PrincipalAdministrator, ID: uuid.New(),
		}, collection, action)
		if err != nil || predicate.kind != predicateAllowAll || !fields.all {
			t.Fatalf("admin %q scope = %#v %#v err=%v", action, predicate, fields, err)
		}
	}
	if err := validateProjection(collection, Predicate{}, AllFields()); !errors.Is(err, ErrProjectionDenied) {
		t.Fatalf("zero predicate error = %v", err)
	}
	if err := validateProjection(collection, AllowAllRecords(), ProjectFields("missing")); !errors.Is(err, ErrProjectionDenied) {
		t.Fatalf("unknown projection error = %v", err)
	}
	if _, err := AllowRecordIDs(uuid.Nil); err == nil {
		t.Fatal("nil record ID predicate accepted")
	}
	if _, _, err := policy.ReadScope(t.Context(), Principal{
		Kind: PrincipalAdministrator, ID: uuid.New(),
	}, collection, ReadAction("future_action")); !errors.Is(err, ErrProjectionDenied) {
		t.Fatalf("unknown read action error = %v, want projection denied", err)
	}
}
