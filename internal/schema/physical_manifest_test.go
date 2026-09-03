package schema

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// compileCatalog is the default-limit shorthand these tests use. The runtime
// compiler always carries the service's own limit.
func compileCatalog(source Catalog) (*compiledCatalog, error) {
	return compileCatalogWithLimit(source, maximumCatalogWireBytes)
}

func TestExpectedPhysicalSchemaForCatalogValidatesInput(t *testing.T) {
	catalog := physicalManifestCatalog(false)
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatalf("compile expected physical schema: %v", err)
	}
	compiled, err := compileCatalog(catalog)
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	if got, want := manifest.Required(), compiled.expectedPhysicalSchema.Required(); !reflect.DeepEqual(got, want) {
		t.Fatalf("public and cached manifest differ\n got: %#v\nwant: %#v", got, want)
	}

	invalid := physicalManifestCatalog(false)
	invalid.Incoming = nil
	if _, err := ExpectedPhysicalSchemaForCatalog(invalid); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid catalog error = %v, want ErrConflict", err)
	}
}

func TestCompileCatalogRejectsRelationStorageOutsidePhysicalManifest(t *testing.T) {
	for _, fieldName := range []string{"author", "tags"} {
		t.Run(fieldName, func(t *testing.T) {
			catalog := physicalManifestCatalog(false)
			posts := catalog.Collections["posts"]
			for index := range posts.Fields {
				if posts.Fields[index].Name == fieldName {
					posts.Fields[index].Relation.Storage.Name = "different_valid_name"
				}
			}
			catalog.Collections["posts"] = posts
			catalog.Incoming = buildIncomingIndex(catalog.Collections)
			if _, err := compileCatalog(catalog); !errors.Is(err, ErrConflict) {
				t.Fatalf("compile catalog error = %v, want ErrConflict", err)
			}
			if _, err := ExpectedPhysicalSchemaForCatalog(catalog); !errors.Is(err, ErrConflict) {
				t.Fatalf("compile expected physical schema error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestCompileCatalogBuildsExpectedPhysicalSchema(t *testing.T) {
	catalog := physicalManifestCatalog(false)
	compiled, err := compileCatalog(catalog)
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	required := compiled.expectedPhysicalSchema.Required()

	if got, want := compiled.expectedPhysicalSchema.ManagedNamespaces(), []string{"app", "pgbase_relation"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed namespaces = %v, want %v", got, want)
	}
	if got, want := len(required.Tables), 4; got != want {
		t.Fatalf("table count = %d, want %d", got, want)
	}
	if got, want := len(required.Columns), 17; got != want {
		t.Fatalf("column count = %d, want %d", got, want)
	}
	if got, want := len(required.Indexes), 12; got != want {
		t.Fatalf("index count = %d, want %d", got, want)
	}
	if got, want := len(required.Constraints), 12; got != want {
		t.Fatalf("constraint count = %d, want %d", got, want)
	}
	if got, want := len(required.Triggers), 3; got != want {
		t.Fatalf("trigger count = %d, want %d", got, want)
	}
	if got, want := len(required.Functions), 2; got != want {
		t.Fatalf("function count = %d, want %d", got, want)
	}
	for _, constraint := range required.Constraints {
		if constraint.Kind == "n" {
			t.Fatalf("PostgreSQL 18 NOT NULL row leaked out of ColumnSpec: %+v", constraint)
		}
	}

	assertPhysicalColumn(t, required, ColumnSpec{
		Namespace: "app", Table: "posts", Name: "title",
		DataType: "text", NotNull: true, DefaultSQL: "E'untitled'::text",
	})
	assertPhysicalColumn(t, required, ColumnSpec{
		Namespace: "app", Table: "posts", Name: "published",
		DataType: "boolean", DefaultSQL: "TRUE",
	})
	assertPhysicalColumn(t, required, ColumnSpec{
		Namespace: "app", Table: "posts", Name: "launched",
		DataType: "timestamp with time zone", DefaultSQL: "CURRENT_TIMESTAMP",
	})
	assertPhysicalColumn(t, required, ColumnSpec{
		Namespace: "app", Table: "posts", Name: "author",
		DataType: "uuid",
	})
	if _, ok := findPhysicalColumn(required, "app", "posts", "tags"); ok {
		t.Fatal("multiple relation unexpectedly produced an app.posts column")
	}

	pageIndex := mustFindPhysicalIndex(t, required, "app", "posts", indexName("posts", "created_at:id"))
	assertIndexShape(t, pageIndex, []string{"created_at", "id"}, false, false, "")
	if got, want := pageIndex.CompatibleNames, []string{"idx_page_726a96b47454863e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page index compatible names = %v, want %v", got, want)
	}
	titleIndex := mustFindPhysicalIndex(t, required, "app", "posts", indexName("posts", "title"))
	assertIndexShape(t, titleIndex, []string{"title"}, false, false, "")
	slugIndex := mustFindPhysicalIndex(t, required, "app", "posts", indexName("posts", "slug"))
	assertIndexShape(t, slugIndex, []string{"slug"}, true, false, "")
	postsPrimary := mustFindPhysicalIndex(t, required, "app", "posts", "posts_pkey")
	assertIndexShape(t, postsPrimary, []string{"id"}, true, true, "posts_pkey")

	names := NewRelationPhysicalNames("posts", "tags")
	assertPhysicalTable(t, required, TableSpec{
		Namespace: "pgbase_relation", Name: names.MultiTable,
		Kind: physicalTableOrdinary, Persistence: physicalTablePermanent,
	})
	assertPhysicalColumn(t, required, ColumnSpec{
		Namespace: "pgbase_relation", Table: names.MultiTable, Name: "position",
		DataType: "integer", NotNull: true,
	})
	positionUnique := mustFindPhysicalIndex(t, required, "pgbase_relation", names.MultiTable, names.MultiPositionUnique)
	assertIndexShape(t, positionUnique, []string{"source_id", "position"}, true, false, names.MultiPositionUnique)
	reverse := mustFindPhysicalIndex(t, required, "pgbase_relation", names.MultiTable, names.MultiReverseIndex)
	assertIndexShape(t, reverse, []string{"target_id", "source_id"}, false, false, "")

	singleNames := NewRelationPhysicalNames("posts", "author")
	singleForeignKey := mustFindPhysicalConstraint(t, required, "app", "posts", singleNames.SingleForeignKey)
	if singleForeignKey.Kind != physicalConstraintForeignKey ||
		!reflect.DeepEqual(singleForeignKey.Columns, []string{"author"}) ||
		singleForeignKey.ReferenceNamespace != "app" ||
		singleForeignKey.ReferenceTable != "users" ||
		!reflect.DeepEqual(singleForeignKey.ReferenceColumns, []string{"id"}) ||
		singleForeignKey.Match != physicalMatchSimple ||
		singleForeignKey.OnUpdate != physicalReferentialNoAction ||
		singleForeignKey.OnDelete != physicalReferentialNoAction ||
		!singleForeignKey.Deferrable || !singleForeignKey.InitiallyDeferred || !singleForeignKey.Validated ||
		!singleForeignKey.Enforced || !singleForeignKey.NoInherit {
		t.Fatalf("single relation foreign key = %+v", singleForeignKey)
	}
	sourceForeignKey := mustFindPhysicalConstraint(t, required, "pgbase_relation", names.MultiTable, names.MultiSourceForeignKey)
	if sourceForeignKey.ReferenceTable != "posts" || sourceForeignKey.OnDelete != physicalReferentialCascade ||
		sourceForeignKey.Deferrable || sourceForeignKey.InitiallyDeferred || !sourceForeignKey.Enforced || !sourceForeignKey.NoInherit {
		t.Fatalf("multi source foreign key = %+v", sourceForeignKey)
	}
	targetForeignKey := mustFindPhysicalConstraint(t, required, "pgbase_relation", names.MultiTable, names.MultiTargetForeignKey)
	if targetForeignKey.ReferenceTable != "tags" || targetForeignKey.OnDelete != physicalReferentialNoAction ||
		!targetForeignKey.Deferrable || !targetForeignKey.InitiallyDeferred || !targetForeignKey.Enforced || !targetForeignKey.NoInherit {
		t.Fatalf("multi target foreign key = %+v", targetForeignKey)
	}
	positionCheck := mustFindPhysicalConstraint(t, required, "pgbase_relation", names.MultiTable, names.MultiPositionCheck)
	if positionCheck.Kind != physicalConstraintCheck || positionCheck.CheckSQL != "position >= 0" ||
		!reflect.DeepEqual(positionCheck.Columns, []string{"position"}) || !positionCheck.Validated ||
		!positionCheck.Enforced || positionCheck.NoInherit {
		t.Fatalf("position check = %+v", positionCheck)
	}

	positionFunction := mustFindPhysicalFunction(t, required, names.PositionFunction)
	wantPositionBody, _ := plannerFunctionBody(positionFunctionStatement("posts", "tags"))
	if positionFunction.BodySQL != wantPositionBody || positionFunction.Namespace != "pgbase_relation" || !positionFunction.ReturnsTrigger ||
		positionFunction.Language != "plpgsql" || positionFunction.Volatility != physicalFunctionVolatile ||
		positionFunction.Kind != physicalFunctionOrdinary || positionFunction.Parallel != physicalFunctionUnsafe ||
		positionFunction.SecurityDefiner || positionFunction.Leakproof || positionFunction.Strict ||
		!strings.Contains(positionFunction.BodySQL, names.MultiPositionCheck) {
		t.Fatalf("position function = %+v", positionFunction)
	}
	cardinalityFunction := mustFindPhysicalFunction(t, required, names.CardinalityFunction)
	wantCardinalityBody, _ := plannerFunctionBody(cardinalityFunctionStatement("posts", "tags", 1, 5, false))
	if cardinalityFunction.BodySQL != wantCardinalityBody || !strings.Contains(cardinalityFunction.BodySQL, "relation_count < 1") ||
		!strings.Contains(cardinalityFunction.BodySQL, "relation_count > 5") {
		t.Fatalf("cardinality function = %+v", cardinalityFunction)
	}

	positionTrigger := mustFindPhysicalTrigger(t, required, "pgbase_relation", names.MultiTable, names.PositionTrigger)
	assertConstraintTrigger(t, positionTrigger, names.PositionFunction,
		physicalTriggerAfterRow|physicalTriggerInsert|physicalTriggerUpdate|physicalTriggerDelete)
	assertTriggerConstraint(t, mustFindPhysicalConstraint(t, required, "pgbase_relation", names.MultiTable, names.PositionTrigger))
	sourceTrigger := mustFindPhysicalTrigger(t, required, "app", "posts", names.SourceCardinalityTrigger)
	assertConstraintTrigger(t, sourceTrigger, names.CardinalityFunction, physicalTriggerAfterRow|physicalTriggerInsert)
	assertTriggerConstraint(t, mustFindPhysicalConstraint(t, required, "app", "posts", names.SourceCardinalityTrigger))
	edgeTrigger := mustFindPhysicalTrigger(t, required, "pgbase_relation", names.MultiTable, names.EdgeCardinalityTrigger)
	assertConstraintTrigger(t, edgeTrigger, names.CardinalityFunction,
		physicalTriggerAfterRow|physicalTriggerInsert|physicalTriggerUpdate|physicalTriggerDelete)
	assertTriggerConstraint(t, mustFindPhysicalConstraint(t, required, "pgbase_relation", names.MultiTable, names.EdgeCardinalityTrigger))
}

func TestExpectedPhysicalSchemaIsDeterministicAndCallerCannotMutateIt(t *testing.T) {
	first, err := compileCatalog(physicalManifestCatalog(false))
	if err != nil {
		t.Fatalf("compile first catalog: %v", err)
	}
	second, err := compileCatalog(physicalManifestCatalog(true))
	if err != nil {
		t.Fatalf("compile second catalog: %v", err)
	}
	firstRequired := first.expectedPhysicalSchema.Required()
	secondRequired := second.expectedPhysicalSchema.Required()
	if !reflect.DeepEqual(firstRequired, secondRequired) {
		t.Fatalf("manifest depends on map insertion order\nfirst:  %#v\nsecond: %#v", firstRequired, secondRequired)
	}
	reorderedCatalog := physicalManifestCatalog(false)
	posts := reorderedCatalog.Collections["posts"]
	for left, right := 0, len(posts.Fields)-1; left < right; left, right = left+1, right-1 {
		posts.Fields[left], posts.Fields[right] = posts.Fields[right], posts.Fields[left]
	}
	for ordinal := range posts.Fields {
		posts.Fields[ordinal].Ordinal = ordinal
	}
	reorderedCatalog.Collections["posts"] = posts
	reorderedCatalog.Incoming = buildIncomingIndex(reorderedCatalog.Collections)
	reordered, err := compileCatalog(reorderedCatalog)
	if err != nil {
		t.Fatalf("compile reordered catalog: %v", err)
	}
	if got := reordered.expectedPhysicalSchema.Required(); !reflect.DeepEqual(got, firstRequired) {
		t.Fatalf("manifest depends on logical field order\nfirst:     %#v\nreordered: %#v", firstRequired, got)
	}
	assertPhysicalManifestOrder(t, firstRequired)

	pristine := first.expectedPhysicalSchema.Required()
	mutated := first.expectedPhysicalSchema.Required()
	mutated.Namespaces[0] = "mutated"
	mutated.Columns[0].Name = "mutated"
	mutated.Indexes[0].Keys[0].Column = "mutated"
	for index := range mutated.Indexes {
		if len(mutated.Indexes[index].CompatibleNames) != 0 {
			mutated.Indexes[index].CompatibleNames[0] = "mutated"
			break
		}
	}
	mutated.Constraints[0].Columns[0] = "mutated"
	for index := range mutated.Constraints {
		if len(mutated.Constraints[index].ReferenceColumns) != 0 {
			mutated.Constraints[index].ReferenceColumns[0] = "mutated"
			break
		}
	}
	mutated.Triggers[0].FunctionArgumentTypes = append(mutated.Triggers[0].FunctionArgumentTypes, "uuid")
	mutated.Functions[0].ArgumentTypes = append(mutated.Functions[0].ArgumentTypes, "uuid")
	namespaces := first.expectedPhysicalSchema.ManagedNamespaces()
	namespaces[0] = "mutated"
	if got := first.expectedPhysicalSchema.Required(); !reflect.DeepEqual(got, pristine) {
		t.Fatalf("caller mutated compiled manifest\n got: %#v\nwant: %#v", got, pristine)
	}
	if got, want := first.expectedPhysicalSchema.ManagedNamespaces(), []string{"app", "pgbase_relation"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated managed namespaces: got %v, want %v", got, want)
	}
}

func TestExpectedPhysicalSchemaOwnershipAllowsUserExtraIndexes(t *testing.T) {
	compiled, err := compileCatalog(physicalManifestCatalog(false))
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	manifest := compiled.expectedPhysicalSchema
	names := NewRelationPhysicalNames("posts", "tags")

	tests := []struct {
		name string
		ref  PhysicalObjectRef
		want bool
	}{
		{name: "required namespace", ref: PhysicalObjectRef{Kind: PhysicalObjectNamespace, Namespace: "app"}, want: true},
		{name: "required primary index", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "posts", Name: "posts_pkey"}, want: true},
		{name: "user extra index", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "posts", Name: "posts_title_trigram"}, want: false},
		{name: "user extra unique index", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "posts", Name: "posts_external_key"}, want: false},
		{name: "reserved scalar index prefix", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "posts", Name: "idx_orphan"}, want: true},
		{name: "reserved single relation index prefix", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "posts", Name: "rel_srev_orphan"}, want: true},
		{name: "user constraint", ref: PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: "app", Table: "posts", Name: "posts_user_check"}, want: false},
		{name: "reserved relation constraint prefix", ref: PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: "app", Table: "posts", Name: "rel_sfk_orphan"}, want: true},
		{name: "reserved source trigger constraint prefix", ref: PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: "app", Table: "posts", Name: "rel_card_src_trg_orphan"}, want: true},
		{name: "user trigger", ref: PhysicalObjectRef{Kind: PhysicalObjectTrigger, Namespace: "app", Table: "posts", Name: "posts_user_trigger"}, want: false},
		{name: "reserved relation trigger prefix", ref: PhysicalObjectRef{Kind: PhysicalObjectTrigger, Namespace: "app", Table: "posts", Name: "rel_card_src_trg_orphan"}, want: true},
		{name: "reserved index prefix on user table", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Table: "analytics", Name: "idx_custom"}, want: false},
		{name: "reserved constraint prefix on user table", ref: PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: "app", Table: "analytics", Name: "rel_sfk_custom"}, want: false},
		{name: "reserved trigger prefix on user table", ref: PhysicalObjectRef{Kind: PhysicalObjectTrigger, Namespace: "app", Table: "analytics", Name: "rel_card_src_trg_custom"}, want: false},
		{name: "uncataloged app table is not blanket owned", ref: PhysicalObjectRef{Kind: PhysicalObjectTable, Namespace: "app", Name: "orphan_collection"}, want: false},
		{name: "uncataloged app column is not blanket owned", ref: PhysicalObjectRef{Kind: PhysicalObjectColumn, Namespace: "app", Table: "posts", Name: "orphan_field"}, want: false},
		{name: "relation namespace index", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "pgbase_relation", Table: names.MultiTable, Name: "user_spelled_name"}, want: true},
		{name: "relation namespace function", ref: PhysicalObjectRef{Kind: PhysicalObjectFunction, Namespace: "pgbase_relation", Name: "user_spelled_name"}, want: true},
		{name: "outside namespace", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "public", Table: "posts", Name: "idx_orphan"}, want: false},
		{name: "malformed child", ref: PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: "app", Name: "idx_orphan"}, want: false},
		{name: "unknown kind", ref: PhysicalObjectRef{Kind: "sequence", Namespace: "app", Name: "anything"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := manifest.Owns(test.ref); got != test.want {
				t.Fatalf("Owns(%+v) = %t, want %t", test.ref, got, test.want)
			}
		})
	}

	assertEveryRequiredObjectOwned(t, manifest)
}

func physicalManifestCatalog(reverse bool) Catalog {
	usersID := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	tagsID := uuid.MustParse("00000000-0000-7000-8000-000000000002")
	postsID := uuid.MustParse("00000000-0000-7000-8000-000000000003")
	createdAt := time.Date(2026, time.September, 1, 1, 2, 3, 456, time.FixedZone("fixture", 9*60*60))

	users := Collection{ID: usersID, Name: "users", TableName: "users", CreatedAt: createdAt}
	tags := Collection{ID: tagsID, Name: "tags", TableName: "tags", CreatedAt: createdAt}
	multiNames := NewRelationPhysicalNames("posts", "tags")
	posts := Collection{
		ID: postsID, Name: "posts", TableName: "posts", CreatedAt: createdAt,
		Fields: []Field{
			{ID: uuid.MustParse("00000000-0000-7000-8000-000000000011"), Name: "title", ColumnName: "title", Type: TypeText, Required: true, Indexed: true, Default: json.RawMessage(`"untitled"`), Ordinal: 0},
			{ID: uuid.MustParse("00000000-0000-7000-8000-000000000012"), Name: "slug", ColumnName: "slug", Type: TypeText, Unique: true, Ordinal: 1},
			{ID: uuid.MustParse("00000000-0000-7000-8000-000000000013"), Name: "published", ColumnName: "published", Type: TypeBoolean, Default: json.RawMessage(`true`), Ordinal: 2},
			{ID: uuid.MustParse("00000000-0000-7000-8000-000000000014"), Name: "launched", ColumnName: "launched", Type: TypeTimestampTZ, Default: json.RawMessage(`{"kind":"now"}`), Ordinal: 3},
			{
				ID: uuid.MustParse("00000000-0000-7000-8000-000000000015"), Name: "author", ColumnName: "author",
				Type: TypeRelation, Unique: true, Indexed: true, Ordinal: 4,
				Relation: &RelationSpec{
					TargetCollectionID: usersID, Cardinality: RelationSingle, MinSelect: 0, MaxSelect: 1,
					OnDelete: RelationRestrict, ValueCodecVersion: RelationValueCodecVersion,
					Storage: RelationStorage{Kind: RelationStorageInlineFKV1, Name: "author"},
				},
			},
			{
				ID: uuid.MustParse("00000000-0000-7000-8000-000000000016"), Name: "tags", ColumnName: "tags",
				Type: TypeRelation, Required: true, Indexed: true, Ordinal: 5,
				Relation: &RelationSpec{
					TargetCollectionID: tagsID, Cardinality: RelationMultiple, MinSelect: 1, MaxSelect: 5,
					OnDelete: RelationRestrict, ValueCodecVersion: RelationValueCodecVersion,
					Storage: RelationStorage{Kind: RelationStorageEdgeTableV1, Name: multiNames.MultiTable},
				},
			},
		},
	}

	collections := make(map[string]Collection, 3)
	if reverse {
		collections["users"] = users
		collections["tags"] = tags
		collections["posts"] = posts
	} else {
		collections["posts"] = posts
		collections["tags"] = tags
		collections["users"] = users
	}
	return Catalog{Version: 7, Collections: collections, Incoming: buildIncomingIndex(collections)}
}

func assertPhysicalColumn(t *testing.T, required RequiredPhysicalObjects, want ColumnSpec) {
	t.Helper()
	got, ok := findPhysicalColumn(required, want.Namespace, want.Table, want.Name)
	if !ok {
		t.Fatalf("column %s.%s.%s not found", want.Namespace, want.Table, want.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("column %s.%s.%s = %+v, want %+v", want.Namespace, want.Table, want.Name, got, want)
	}
}

func findPhysicalColumn(required RequiredPhysicalObjects, namespace, table, name string) (ColumnSpec, bool) {
	for _, column := range required.Columns {
		if column.Namespace == namespace && column.Table == table && column.Name == name {
			return column, true
		}
	}
	return ColumnSpec{}, false
}

func assertPhysicalTable(t *testing.T, required RequiredPhysicalObjects, want TableSpec) {
	t.Helper()
	for _, table := range required.Tables {
		if table.Namespace == want.Namespace && table.Name == want.Name {
			if !reflect.DeepEqual(table, want) {
				t.Fatalf("table %s.%s = %+v, want %+v", want.Namespace, want.Name, table, want)
			}
			return
		}
	}
	t.Fatalf("table %s.%s not found", want.Namespace, want.Name)
}

func mustFindPhysicalIndex(t *testing.T, required RequiredPhysicalObjects, namespace, table, name string) IndexSpec {
	t.Helper()
	for _, index := range required.Indexes {
		if index.Namespace == namespace && index.Table == table && index.Name == name {
			return index
		}
	}
	t.Fatalf("index %s.%s.%s not found", namespace, table, name)
	return IndexSpec{}
}

func assertIndexShape(t *testing.T, index IndexSpec, columns []string, unique, primary bool, constraintName string) {
	t.Helper()
	gotColumns := make([]string, len(index.Keys))
	for position, key := range index.Keys {
		gotColumns[position] = key.Column
		if key.Descending || key.NullsFirst || key.OperatorClass != "" {
			t.Fatalf("index %s key %d = %+v", index.Name, position, key)
		}
	}
	if !reflect.DeepEqual(gotColumns, columns) || index.Method != "btree" || index.Unique != unique || index.NullsNotDistinct ||
		index.Primary != primary || index.ConstraintName != constraintName || len(index.IncludedColumns) != 0 ||
		index.PredicateSQL != "" || index.ExpressionSQL != "" || !index.Valid || !index.Ready || !index.Live {
		t.Fatalf("index %s = %+v, columns %v", index.Name, index, gotColumns)
	}
}

func mustFindPhysicalConstraint(t *testing.T, required RequiredPhysicalObjects, namespace, table, name string) ConstraintSpec {
	t.Helper()
	for _, constraint := range required.Constraints {
		if constraint.Namespace == namespace && constraint.Table == table && constraint.Name == name {
			return constraint
		}
	}
	t.Fatalf("constraint %s.%s.%s not found", namespace, table, name)
	return ConstraintSpec{}
}

func mustFindPhysicalFunction(t *testing.T, required RequiredPhysicalObjects, name string) FunctionSpec {
	t.Helper()
	for _, function := range required.Functions {
		if function.Namespace == relationPhysicalNamespace && function.Name == name {
			return function
		}
	}
	t.Fatalf("function %s.%s not found", relationPhysicalNamespace, name)
	return FunctionSpec{}
}

func mustFindPhysicalTrigger(t *testing.T, required RequiredPhysicalObjects, namespace, table, name string) TriggerSpec {
	t.Helper()
	for _, trigger := range required.Triggers {
		if trigger.Namespace == namespace && trigger.Table == table && trigger.Name == name {
			return trigger
		}
	}
	t.Fatalf("trigger %s.%s.%s not found", namespace, table, name)
	return TriggerSpec{}
}

func assertConstraintTrigger(t *testing.T, trigger TriggerSpec, functionName string, typeMask int) {
	t.Helper()
	if trigger.FunctionNamespace != relationPhysicalNamespace || trigger.FunctionName != functionName ||
		len(trigger.FunctionArgumentTypes) != 0 || trigger.TypeMask != typeMask || len(trigger.UpdateColumns) != 0 ||
		trigger.Enabled != physicalTriggerEnabledOrigin || !trigger.Constraint || !trigger.Deferrable ||
		!trigger.InitiallyDeferred || len(trigger.Arguments) != 0 || trigger.WhenSQL != "" ||
		trigger.OldTransitionTable != "" || trigger.NewTransitionTable != "" {
		t.Fatalf("trigger %s = %+v", trigger.Name, trigger)
	}
}

func assertTriggerConstraint(t *testing.T, constraint ConstraintSpec) {
	t.Helper()
	if constraint.Kind != physicalConstraintTrigger || len(constraint.Columns) != 0 ||
		constraint.ReferenceNamespace != "" || constraint.ReferenceTable != "" || len(constraint.ReferenceColumns) != 0 ||
		constraint.Match != physicalReferentialUnused || constraint.OnUpdate != physicalReferentialUnused ||
		constraint.OnDelete != physicalReferentialUnused || constraint.CheckSQL != "" ||
		!constraint.Deferrable || !constraint.InitiallyDeferred || !constraint.Validated || !constraint.Enforced || !constraint.NoInherit {
		t.Fatalf("trigger constraint %s = %+v", constraint.Name, constraint)
	}
}

func assertPhysicalManifestOrder(t *testing.T, required RequiredPhysicalObjects) {
	t.Helper()
	assertSorted := func(name string, size int, less func(int, int) bool) {
		t.Helper()
		for index := 1; index < size; index++ {
			if less(index, index-1) {
				t.Fatalf("%s are not deterministically sorted at %d", name, index)
			}
		}
	}
	assertSorted("namespaces", len(required.Namespaces), func(i, j int) bool {
		return required.Namespaces[i] < required.Namespaces[j]
	})
	assertSorted("tables", len(required.Tables), func(i, j int) bool {
		left, right := required.Tables[i], required.Tables[j]
		return physicalLess(left.Namespace, "", left.Name, right.Namespace, "", right.Name)
	})
	assertSorted("columns", len(required.Columns), func(i, j int) bool {
		left, right := required.Columns[i], required.Columns[j]
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		if left.Table != right.Table {
			return left.Table < right.Table
		}
		return left.Name < right.Name
	})
	assertSorted("indexes", len(required.Indexes), func(i, j int) bool {
		left, right := required.Indexes[i], required.Indexes[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	assertSorted("constraints", len(required.Constraints), func(i, j int) bool {
		left, right := required.Constraints[i], required.Constraints[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	assertSorted("triggers", len(required.Triggers), func(i, j int) bool {
		left, right := required.Triggers[i], required.Triggers[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	assertSorted("functions", len(required.Functions), func(i, j int) bool {
		left, right := required.Functions[i], required.Functions[j]
		return physicalLess(left.Namespace, "", left.Name, right.Namespace, "", right.Name)
	})
}

func assertEveryRequiredObjectOwned(t *testing.T, manifest ExpectedPhysicalSchema) {
	t.Helper()
	required := manifest.Required()
	refs := make([]PhysicalObjectRef, 0,
		len(required.Namespaces)+len(required.Tables)+len(required.Columns)+len(required.Indexes)+
			len(required.Constraints)+len(required.Triggers)+len(required.Functions))
	for _, namespace := range required.Namespaces {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectNamespace, Namespace: namespace})
	}
	for _, table := range required.Tables {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectTable, Namespace: table.Namespace, Name: table.Name})
	}
	for _, column := range required.Columns {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectColumn, Namespace: column.Namespace, Table: column.Table, Name: column.Name})
	}
	for _, index := range required.Indexes {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: index.Namespace, Table: index.Table, Name: index.Name})
	}
	for _, constraint := range required.Constraints {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: constraint.Namespace, Table: constraint.Table, Name: constraint.Name})
	}
	for _, trigger := range required.Triggers {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectTrigger, Namespace: trigger.Namespace, Table: trigger.Table, Name: trigger.Name})
	}
	for _, function := range required.Functions {
		refs = append(refs, PhysicalObjectRef{Kind: PhysicalObjectFunction, Namespace: function.Namespace, Name: function.Name})
	}
	for _, ref := range refs {
		if !manifest.Owns(ref) {
			t.Errorf("required object is not owned: %+v", ref)
		}
	}
}

// The companion set must be a pure function of the catalog, so this pins both
// which fields earn one and the exact key directions PostgreSQL has to store.
func TestCompanionIndexesCoverOnlyNullableDescendingSortKeys(t *testing.T) {
	compiled, err := compileCatalog(physicalManifestCatalog(false))
	if err != nil {
		t.Fatalf("compile catalog: %v", err)
	}
	manifest := compiled.expectedPhysicalSchema

	// posts.slug is a nullable unique text field and posts.author a nullable
	// single relation; both are sortable and both can hold NULL. posts.title is
	// required, posts.published and posts.launched are unindexed, and posts.tags
	// is a multiple relation the read path refuses to sort, so none of those may
	// contribute a companion.
	want := []IndexSpec{
		companionIndexSpec("posts", "author", "author"),
		companionIndexSpec("posts", "slug", "slug"),
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	if got := manifest.CompanionIndexes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("companion indexes\n got: %#v\nwant: %#v", got, want)
	}

	for _, companion := range want {
		for _, required := range manifest.Required().Indexes {
			if required.Name == companion.Name {
				t.Fatalf("companion %s must not be a required index before the capability is ready", companion.Name)
			}
		}
		ref := PhysicalObjectRef{
			Kind: PhysicalObjectIndex, Namespace: companion.Namespace,
			Table: companion.Table, Name: companion.Name,
		}
		if !manifest.Owns(ref) {
			t.Fatalf("companion %s is not owned; a rollback binary would call it an orphan", companion.Name)
		}
	}

	for _, required := range manifest.Required().Indexes {
		if required.Purpose != IndexPurposeCore {
			t.Fatalf("required index %s purpose = %d, want IndexPurposeCore", required.Name, required.Purpose)
		}
	}
}

func TestCompanionIndexesAreImmutableAndDeterministic(t *testing.T) {
	first, err := compileCatalog(physicalManifestCatalog(false))
	if err != nil {
		t.Fatalf("compile first catalog: %v", err)
	}
	second, err := compileCatalog(physicalManifestCatalog(true))
	if err != nil {
		t.Fatalf("compile second catalog: %v", err)
	}
	pristine := first.expectedPhysicalSchema.CompanionIndexes()
	if got := second.expectedPhysicalSchema.CompanionIndexes(); !reflect.DeepEqual(got, pristine) {
		t.Fatalf("companion indexes depend on map insertion order\nfirst:  %#v\nsecond: %#v", pristine, got)
	}
	if len(pristine) == 0 {
		t.Fatal("fixture must produce at least one companion index")
	}
	mutated := first.expectedPhysicalSchema.CompanionIndexes()
	mutated[0].Name = "mutated"
	mutated[0].Keys[0].Column = "mutated"
	mutated[0].Keys[0].NullsFirst = !mutated[0].Keys[0].NullsFirst
	if got := first.expectedPhysicalSchema.CompanionIndexes(); !reflect.DeepEqual(got, pristine) {
		t.Fatalf("caller mutated compiled companion indexes\n got: %#v\nwant: %#v", got, pristine)
	}
}

func companionIndexSpec(collection, field, column string) IndexSpec {
	return IndexSpec{
		Namespace: "app",
		Table:     collection,
		Name:      NullableDescCompanionIndexName(collection, field),
		Method:    "btree",
		Keys: []IndexKeySpec{
			{Column: column, Descending: true, NullsFirst: false},
			{Column: "id", Descending: true, NullsFirst: true},
		},
		Valid:   true,
		Ready:   true,
		Live:    true,
		Purpose: IndexPurposeNullableDesc,
	}
}
