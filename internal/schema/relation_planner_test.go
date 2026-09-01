package schema

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	relationPlannerPostsID = uuid.MustParse("019b0000-0000-7000-8000-000000000001")
	relationPlannerUsersID = uuid.MustParse("019b0000-0000-7000-8000-000000000002")
	relationPlannerTagsID  = uuid.MustParse("019b0000-0000-7000-8000-000000000003")
)

func TestRelationPlanCreateWithCatalogBuildsDeterministicHybridDDL(t *testing.T) {
	t.Parallel()

	catalog := relationPlannerCatalog()
	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true},
			relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict),
			relationPlannerField("tags", "tags", RelationMultiple, false, 20, RelationSetNull),
		},
	}
	planner := Planner{}
	first, err := planner.PlanCreateWithCatalog(11, catalog, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.PlanCreateWithCatalog(11, catalog, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || !reflect.DeepEqual(first, second) {
		t.Fatalf("relation plans are not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}

	author := first.Definition.Fields[1]
	if author.Relation == nil || author.Relation.TargetCollectionID == nil || *author.Relation.TargetCollectionID != relationPlannerUsersID {
		t.Fatalf("author target binding = %#v, want users ID", author.Relation)
	}
	if !author.Indexed || author.Relation.Storage != (RelationStorage{Kind: RelationStorageInlineFKV1, Name: "author"}) {
		t.Fatalf("author normalization = %#v, want indexed inline storage", author)
	}
	tags := first.Definition.Fields[2]
	names := NewRelationPhysicalNames("posts", "tags")
	if tags.Relation == nil || tags.Relation.TargetCollectionID == nil || *tags.Relation.TargetCollectionID != relationPlannerTagsID {
		t.Fatalf("tags target binding = %#v, want tags ID", tags.Relation)
	}
	if tags.Relation.Storage != (RelationStorage{Kind: RelationStorageEdgeTableV1, Name: names.MultiTable}) {
		t.Fatalf("tags storage = %#v, want deterministic edge table", tags.Relation.Storage)
	}

	joined := strings.Join(first.Statements, "\n")
	for _, fragment := range []string{
		`CREATE TABLE "app"."posts"`,
		`"author" uuid`,
		`REFERENCES "app"."users" ("id") ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`,
		`CREATE INDEX "` + NewRelationPhysicalNames("posts", "author").SingleReverseIndex + `"`,
		`CREATE TABLE "pgbase_relation"."` + names.MultiTable + `"`,
		`CONSTRAINT "` + names.MultiPrimaryKey + `" PRIMARY KEY ("source_id", "target_id")`,
		`CONSTRAINT "` + names.MultiPositionUnique + `" UNIQUE ("source_id", "position")`,
		`CONSTRAINT "` + names.MultiPositionCheck + `" CHECK ("position" >= 0)`,
		`ON DELETE CASCADE`,
		`ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`,
		`CREATE CONSTRAINT TRIGGER "` + names.PositionTrigger + `"`,
		`CREATE CONSTRAINT TRIGGER "` + names.SourceCardinalityTrigger + `"`,
		`CREATE CONSTRAINT TRIGGER "` + names.EdgeCardinalityTrigger + `"`,
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("DDL does not contain %q:\n%s", fragment, joined)
		}
	}
	if strings.Contains(first.Statements[0], `"tags" relation`) || strings.Contains(first.Statements[0], `"tags" uuid`) {
		t.Fatalf("multi relation leaked into source columns: %s", first.Statements[0])
	}
	if len(first.Statements) != 11 {
		t.Fatalf("statement count = %d, want 11: %#v", len(first.Statements), first.Statements)
	}
}

func TestRelationPlanCreatePreservesSymbolicSelfTarget(t *testing.T) {
	t.Parallel()

	input := CreateCollectionInput{
		Name: "nodes",
		Fields: []FieldInput{
			relationPlannerField("parent", "nodes", RelationSingle, false, 0, RelationSetNull),
		},
	}
	plan, err := (Planner{}).PlanCreateWithCatalog(3, Catalog{Collections: map[string]Collection{}}, input)
	if err != nil {
		t.Fatal(err)
	}
	relation := plan.Definition.Fields[0].Relation
	if relation == nil || relation.TargetCollectionID != nil || relation.TargetCollection != "nodes" {
		t.Fatalf("self relation = %#v, want symbolic nodes target", relation)
	}
	if joined := strings.Join(plan.Statements, "\n"); !strings.Contains(joined, `REFERENCES "app"."nodes" ("id")`) {
		t.Fatalf("self FK does not target source table:\n%s", joined)
	}
}

func TestRelationPlanCreateRejectsMissingExternalTarget(t *testing.T) {
	t.Parallel()

	_, err := (Planner{}).PlanCreateWithCatalog(0, Catalog{Collections: map[string]Collection{}}, CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			relationPlannerField("author", "missing_users", RelationSingle, false, 0, RelationRestrict),
		},
	})
	assertRelationPlannerValidation(t, err, ValidationDetail{
		Field: "fields.0.relation.targetCollection",
		Code:  "relation_target_collection_not_found",
	})
}

func TestRelationPlanCreateReusesDomainValidation(t *testing.T) {
	t.Parallel()

	zero := 0
	one := 1
	two := 2
	cases := []struct {
		name  string
		field FieldInput
		want  ValidationDetail
	}{
		{
			name: "relation config on scalar",
			field: FieldInput{Name: "title", Type: TypeText, Relation: &RelationInput{
				TargetCollection: "users",
			}},
			want: ValidationDetail{Field: "fields.0.relation", Code: "relation_config_forbidden"},
		},
		{
			name:  "relation default",
			field: relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict),
			want:  ValidationDetail{Field: "fields.0.default", Code: "relation_default_unsupported"},
		},
		{
			name:  "multiple requires maximum",
			field: relationPlannerField("tags", "tags", RelationMultiple, false, 0, RelationRestrict),
			want:  ValidationDetail{Field: "fields.0.relation.maxSelect", Code: "relation_invalid_bounds"},
		},
		{
			name:  "required mismatch",
			field: relationPlannerField("author", "users", RelationSingle, true, 1, RelationRestrict),
			want:  ValidationDetail{Field: "fields.0.relation.minSelect", Code: "relation_required_mismatch"},
		},
		{
			name:  "set null required",
			field: relationPlannerField("author", "users", RelationSingle, true, 0, RelationSetNull),
			want:  ValidationDetail{Field: "fields.0.relation.onDelete", Code: "relation_set_null_requires_optional"},
		},
		{
			name:  "unique multiple",
			field: relationPlannerField("tags", "tags", RelationMultiple, false, 2, RelationRestrict),
			want:  ValidationDetail{Field: "fields.0.unique", Code: "relation_unique_requires_single"},
		},
	}
	cases[1].field.Default = json.RawMessage(`"not-allowed"`)
	cases[2].field.Relation.MaxSelect = nil
	cases[3].field.Relation.MinSelect = &zero
	cases[3].field.Relation.MaxSelect = &one
	cases[4].field.Relation.MinSelect = &one
	cases[4].field.Relation.MaxSelect = &one
	cases[5].field.Unique = true
	cases[5].field.Relation.MaxSelect = &two

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := (Planner{}).PlanCreateWithCatalog(0, relationPlannerCatalog(), CreateCollectionInput{
				Name:   "posts",
				Fields: []FieldInput{test.field},
			})
			assertRelationPlannerValidation(t, err, test.want)
		})
	}
}

func TestRelationPlanUpdateAllowsSingleFlagsAndDeleteAction(t *testing.T) {
	t.Parallel()

	current, catalog := relationPlannerCurrentSingle()
	field := relationPlannerField("author", "users", RelationSingle, true, 0, RelationCascade)
	field.Unique = true
	plan, err := (Planner{}).PlanUpdateWithCatalog(8, catalog, current, UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{field},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := NewRelationPhysicalNames("posts", "author")
	want := []string{
		`ALTER TABLE "app"."posts" ALTER COLUMN "author" SET NOT NULL`,
		`DROP INDEX IF EXISTS "app"."` + names.SingleReverseIndex + `"`,
		`CREATE UNIQUE INDEX "` + names.SingleReverseIndex + `" ON "app"."posts" ("author")`,
	}
	if !reflect.DeepEqual(plan.Statements, want) {
		t.Fatalf("single update statements = %#v, want %#v", plan.Statements, want)
	}
	if got := plan.Definition.Fields[0].Relation.OnDelete; got != RelationCascade {
		t.Fatalf("normalized onDelete = %q, want cascade", got)
	}
}

func TestRelationPlanUpdateAllowsMultiBoundsAndMetadataOnlyDeleteAction(t *testing.T) {
	t.Parallel()

	current, catalog := relationPlannerCurrentMulti()
	changedBounds := relationPlannerField("tags", "tags", RelationMultiple, false, 10, RelationCascade)
	plan, err := (Planner{}).PlanUpdateWithCatalog(9, catalog, current, UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{changedBounds},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 2 || !strings.HasPrefix(plan.Statements[0], "DO $pgbase_relation$") ||
		!strings.HasPrefix(plan.Statements[1], `CREATE OR REPLACE FUNCTION "pgbase_relation".`) {
		t.Fatalf("multi bound statements = %#v, want validation then function replacement", plan.Statements)
	}
	if !strings.Contains(plan.Statements[0], "> 10") || !strings.Contains(plan.Statements[1], "relation_count > 10") {
		t.Fatalf("new maximum is absent from bound DDL: %#v", plan.Statements)
	}

	onlyDeleteAction := relationPlannerField("tags", "tags", RelationMultiple, false, 5, RelationSetNull)
	metadataOnly, err := (Planner{}).PlanUpdateWithCatalog(9, catalog, current, UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{onlyDeleteAction},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadataOnly.Statements) != 0 {
		t.Fatalf("onDelete-only update emitted DDL: %#v", metadataOnly.Statements)
	}
	if metadataOnly.Definition.Fields[0].Relation.OnDelete != RelationSetNull {
		t.Fatalf("onDelete metadata was not updated: %#v", metadataOnly.Definition.Fields[0].Relation)
	}
}

func TestRelationPlanUpdateRejectsStorageConversionsAndUnsafeAdds(t *testing.T) {
	t.Parallel()

	current, catalog := relationPlannerCurrentSingle()
	cases := []struct {
		name  string
		input UpdateCollectionInput
		want  ValidationDetail
	}{
		{
			name: "target change",
			input: UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
				relationPlannerField("author", "tags", RelationSingle, false, 0, RelationRestrict),
			}},
			want: ValidationDetail{Field: "fields.0.relation", Code: "relation_storage_migration_required"},
		},
		{
			name: "single to multiple",
			input: UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
				relationPlannerField("author", "users", RelationMultiple, false, 5, RelationRestrict),
			}},
			want: ValidationDetail{Field: "fields.0.relation", Code: "relation_storage_migration_required"},
		},
		{
			name: "relation to scalar",
			input: UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
				{Name: "author", Type: TypeUUID},
			}},
			want: ValidationDetail{Field: "fields.0.type", Code: "relation_storage_migration_required"},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := (Planner{}).PlanUpdateWithCatalog(8, catalog, current, test.input)
			assertRelationPlannerValidation(t, err, test.want)
		})
	}
}

func TestRelationPlannerDefersRequiredRelationAddRowCheckToService(t *testing.T) {
	t.Parallel()
	current, catalog := relationPlannerCurrentSingle()
	plan, err := (Planner{}).PlanUpdateWithCatalog(8, catalog, current, UpdateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict),
			relationPlannerField("reviewer", "users", RelationSingle, true, 0, RelationCascade),
		},
	})
	if err != nil {
		t.Fatalf("PlanUpdateWithCatalog(required relation): %v", err)
	}
	want := `ALTER TABLE "app"."posts" ADD COLUMN "reviewer" uuid not null`
	if !slices.Contains(plan.Statements, want) {
		t.Fatalf("required relation statements = %#v, want %q", plan.Statements, want)
	}
}

func TestRelationPlanUpdateDropsPhysicalObjectsBeforeMetadataApply(t *testing.T) {
	t.Parallel()

	single, singleCatalog := relationPlannerCurrentSingle()
	plan, err := (Planner{}).PlanUpdateWithCatalog(4, singleCatalog, single, UpdateCollectionInput{Name: "posts", Fields: []FieldInput{}})
	if err != nil {
		t.Fatal(err)
	}
	singleNames := NewRelationPhysicalNames("posts", "author")
	wantSingle := []string{
		`ALTER TABLE "app"."posts" DROP CONSTRAINT "` + singleNames.SingleForeignKey + `"`,
		`DROP INDEX IF EXISTS "app"."` + singleNames.SingleReverseIndex + `"`,
		`ALTER TABLE "app"."posts" DROP COLUMN "author"`,
	}
	if !reflect.DeepEqual(plan.Statements, wantSingle) {
		t.Fatalf("single drop statements = %#v, want %#v", plan.Statements, wantSingle)
	}

	multi, multiCatalog := relationPlannerCurrentMulti()
	plan, err = (Planner{}).PlanUpdateWithCatalog(4, multiCatalog, multi, UpdateCollectionInput{Name: "posts", Fields: []FieldInput{}})
	if err != nil {
		t.Fatal(err)
	}
	multiNames := NewRelationPhysicalNames("posts", "tags")
	joined := strings.Join(plan.Statements, "\n")
	if len(plan.Statements) != 6 || !strings.Contains(joined, `DROP TABLE "pgbase_relation"."`+multiNames.MultiTable+`"`) ||
		!strings.Contains(joined, `DROP FUNCTION "pgbase_relation"."`+multiNames.CardinalityFunction+`"()`) {
		t.Fatalf("multi drop statements = %#v", plan.Statements)
	}
}

func TestRelationPlanDeleteGuardsIncomingAndCleansOutgoingMulti(t *testing.T) {
	t.Parallel()

	current, catalog := relationPlannerCurrentMulti()
	catalog.Incoming = make(map[uuid.UUID][]RelationRef)
	catalog.Incoming[current.ID] = []RelationRef{{SourceCollectionID: relationPlannerUsersID}}
	_, err := (Planner{}).PlanDeleteWithCatalog(5, catalog, current)
	assertRelationPlannerValidation(t, err, ValidationDetail{Field: "name", Code: "collection_referenced"})

	delete(catalog.Incoming, current.ID)
	plan, err := (Planner{}).PlanDeleteWithCatalog(5, catalog, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 7 || plan.Statements[len(plan.Statements)-1] != `DROP TABLE "app"."posts"` {
		t.Fatalf("outgoing cleanup/delete order = %#v", plan.Statements)
	}

	catalog.Incoming[current.ID] = []RelationRef{{SourceCollectionID: current.ID}}
	if _, err := (Planner{}).PlanDeleteWithCatalog(5, catalog, current); err != nil {
		t.Fatalf("self relation incorrectly blocked collection deletion: %v", err)
	}
}

func TestRelationPlanSupportsCatalogCycles(t *testing.T) {
	t.Parallel()

	posts := Collection{ID: relationPlannerPostsID, Name: "posts", TableName: "posts"}
	users := Collection{
		ID: relationPlannerUsersID, Name: "users", TableName: "users",
		Fields: []Field{{
			ID:   uuid.MustParse("019b0000-0000-7000-8000-000000000020"),
			Name: "featured_post", ColumnName: "featured_post", Type: TypeRelation, Indexed: true,
			Relation: &RelationSpec{
				TargetCollectionID: relationPlannerPostsID,
				Cardinality:        RelationSingle, MinSelect: 0, MaxSelect: 1, OnDelete: RelationSetNull,
				ValueCodecVersion: RelationValueCodecVersion,
				Storage:           RelationStorage{Kind: RelationStorageInlineFKV1, Name: "featured_post"},
			},
		}},
	}
	catalog := Catalog{Collections: map[string]Collection{"posts": posts, "users": users}}
	plan, err := (Planner{}).PlanUpdateWithCatalog(2, catalog, posts, UpdateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Definition.Fields[0].Relation == nil || *plan.Definition.Fields[0].Relation.TargetCollectionID != relationPlannerUsersID {
		t.Fatalf("cycle target was not resolved: %#v", plan.Definition.Fields[0].Relation)
	}
}

func TestRelationPlanCatalogAwareScalarPathIsCompatibleAndRejectsHostileNames(t *testing.T) {
	t.Parallel()

	input := CreateCollectionInput{Name: "articles", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	legacy, err := (Planner{}).PlanCreate(3, input)
	if err != nil {
		t.Fatal(err)
	}
	aware, err := (Planner{}).PlanCreateWithCatalog(3, relationPlannerCatalog(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy, aware) {
		t.Fatalf("catalog-aware scalar plan changed compatibility:\nlegacy: %#v\naware:  %#v", legacy, aware)
	}

	for _, invalid := range []CreateCollectionInput{
		{Name: `posts";drop_table`, Fields: []FieldInput{relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict)}},
		{Name: "posts", Fields: []FieldInput{relationPlannerField(`author";drop`, "users", RelationSingle, false, 0, RelationRestrict)}},
	} {
		plan, err := (Planner{}).PlanCreateWithCatalog(3, relationPlannerCatalog(), invalid)
		if err == nil || len(plan.Statements) != 0 {
			t.Fatalf("hostile identifier reached DDL: plan=%#v err=%v", plan, err)
		}
	}
}

func relationPlannerCatalog() Catalog {
	return Catalog{Collections: map[string]Collection{
		"users": {ID: relationPlannerUsersID, Name: "users", TableName: "users"},
		"tags":  {ID: relationPlannerTagsID, Name: "tags", TableName: "tags"},
	}}
}

func relationPlannerField(
	name, target string,
	cardinality RelationCardinality,
	required bool,
	maximum int,
	onDelete RelationDeleteAction,
) FieldInput {
	field := FieldInput{
		Name: name, Type: TypeRelation, Required: required,
		Relation: &RelationInput{
			TargetCollection: target,
			Cardinality:      cardinality,
			OnDelete:         onDelete,
		},
	}
	if maximum > 0 {
		field.Relation.MaxSelect = intPointer(maximum)
	}
	return field
}

func relationPlannerCurrentSingle() (Collection, Catalog) {
	current := Collection{
		ID: relationPlannerPostsID, Name: "posts", TableName: "posts",
		Fields: []Field{{
			ID:   uuid.MustParse("019b0000-0000-7000-8000-000000000010"),
			Name: "author", ColumnName: "author", Type: TypeRelation, Indexed: true,
			Relation: &RelationSpec{
				TargetCollectionID: relationPlannerUsersID,
				Cardinality:        RelationSingle, MinSelect: 0, MaxSelect: 1, OnDelete: RelationRestrict,
				ValueCodecVersion: RelationValueCodecVersion,
				Storage:           RelationStorage{Kind: RelationStorageInlineFKV1, Name: "author"},
			},
		}},
	}
	catalog := relationPlannerCatalog()
	catalog.Collections["posts"] = current
	return current, catalog
}

func relationPlannerCurrentMulti() (Collection, Catalog) {
	names := NewRelationPhysicalNames("posts", "tags")
	current := Collection{
		ID: relationPlannerPostsID, Name: "posts", TableName: "posts",
		Fields: []Field{{
			ID:   uuid.MustParse("019b0000-0000-7000-8000-000000000011"),
			Name: "tags", ColumnName: "tags", Type: TypeRelation, Indexed: true,
			Relation: &RelationSpec{
				TargetCollectionID: relationPlannerTagsID,
				Cardinality:        RelationMultiple, MinSelect: 0, MaxSelect: 5, OnDelete: RelationRestrict,
				ValueCodecVersion: RelationValueCodecVersion,
				Storage:           RelationStorage{Kind: RelationStorageEdgeTableV1, Name: names.MultiTable},
			},
		}},
	}
	catalog := relationPlannerCatalog()
	catalog.Collections["posts"] = current
	return current, catalog
}

func assertRelationPlannerValidation(t *testing.T, err error, want ValidationDetail) {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) || !reflect.DeepEqual(validation.Details, []ValidationDetail{want}) {
		t.Fatalf("validation error = %#v, want %#v", err, []ValidationDetail{want})
	}
}
