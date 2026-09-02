//go:build integration

package schema

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestRelationServiceCreatesExistingTargetSingleAndMultipleRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	users := applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "users",
		Fields: []FieldInput{{Name: "email", Type: TypeText}},
	}, administratorID)

	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{
				Name: "owner", Type: TypeRelation,
				Relation: &RelationInput{TargetCollection: "users"},
			},
			{
				Name: "members", Type: TypeRelation,
				Relation: &RelationInput{
					TargetCollection: "users",
					Cardinality:      RelationMultiple,
					MaxSelect:        intPointer(20),
					OnDelete:         RelationCascade,
				},
			},
		},
	}
	plan, err := service.PlanCreate(ctx, input)
	if err != nil {
		t.Fatalf("PlanCreate(): %v", err)
	}
	if len(plan.Definition.Fields) != 2 || plan.Definition.Fields[0].Relation == nil || plan.Definition.Fields[1].Relation == nil {
		t.Fatalf("planned relation fields = %#v", plan.Definition.Fields)
	}
	if plan.Definition.Fields[0].Relation.TargetCollectionID == nil ||
		*plan.Definition.Fields[0].Relation.TargetCollectionID != users.ID ||
		plan.Definition.Fields[1].Relation.TargetCollectionID == nil ||
		*plan.Definition.Fields[1].Relation.TargetCollectionID != users.ID {
		t.Fatalf("planned targets do not resolve to users %s: %#v", users.ID, plan.Definition.Fields)
	}

	created, err := service.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyCreate(): %v", err)
	}
	if len(created.Fields) != 2 || created.Fields[0].Relation == nil || created.Fields[1].Relation == nil {
		t.Fatalf("created relation fields = %#v", created.Fields)
	}
	if created.Fields[0].Relation.TargetCollectionID != users.ID ||
		created.Fields[0].Relation.Storage != (RelationStorage{Kind: RelationStorageInlineFKV1, Name: "owner"}) {
		t.Fatalf("created single relation = %#v", created.Fields[0].Relation)
	}
	multiNames := NewRelationPhysicalNames("posts", "members")
	if created.Fields[1].Relation.TargetCollectionID != users.ID ||
		created.Fields[1].Relation.Storage != (RelationStorage{Kind: RelationStorageEdgeTableV1, Name: multiNames.MultiTable}) {
		t.Fatalf("created multiple relation = %#v", created.Fields[1].Relation)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog(): %v", err)
	}
	if catalog.Version != 2 || len(catalog.Incoming[users.ID]) != 2 {
		t.Fatalf("relation catalog version/incoming = %d/%#v", catalog.Version, catalog.Incoming)
	}
	if err := validateCatalogIntegrity(catalog); err != nil {
		t.Fatalf("published catalog integrity: %v", err)
	}

	var ownerType string
	if err := pool.QueryRow(ctx, `select data_type from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'owner'`).Scan(&ownerType); err != nil {
		t.Fatalf("read single relation column: %v", err)
	}
	if ownerType != "uuid" {
		t.Fatalf("single relation column type = %q, want uuid", ownerType)
	}
	var multiColumns int
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'members'`).Scan(&multiColumns); err != nil {
		t.Fatal(err)
	}
	if multiColumns != 0 {
		t.Fatalf("multiple relation created %d inline columns, want 0", multiColumns)
	}
	var edgeTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass($1)::text, '')`, "pgbase_relation."+multiNames.MultiTable).Scan(&edgeTable); err != nil {
		t.Fatalf("read edge table: %v", err)
	}
	if edgeTable == "" {
		t.Fatal("multiple relation edge table does not exist")
	}
	var relationRows int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.field_relations where source_collection_id = $1`, created.ID).Scan(&relationRows); err != nil {
		t.Fatal(err)
	}
	if relationRows != 2 {
		t.Fatalf("persisted relation metadata rows = %d, want 2", relationRows)
	}
}

func TestRelationServiceResolvesSelfRelationAndPublishesListenerCatalog(t *testing.T) {
	ctx := context.Background()
	_, service, administratorID := newSchemaServiceTest(t, Planner{})
	listenerCache := &Cache{}
	listener := NewListener(service.pool, listenerCache)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- listener.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("listener Run(): %v", err)
		}
	})
	waitForListenerReady(t, listener)

	input := CreateCollectionInput{
		Name: "nodes",
		Fields: []FieldInput{{
			Name: "parent", Type: TypeRelation,
			Relation: &RelationInput{TargetCollection: "nodes", OnDelete: RelationSetNull},
		}},
	}
	plan, err := service.PlanCreate(ctx, input)
	if err != nil {
		t.Fatalf("PlanCreate(): %v", err)
	}
	if plan.Definition.Fields[0].Relation == nil || plan.Definition.Fields[0].Relation.TargetCollectionID != nil {
		t.Fatalf("self relation plan must remain symbolic: %#v", plan.Definition.Fields[0].Relation)
	}
	created, err := service.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyCreate(): %v", err)
	}
	if created.Fields[0].Relation == nil || created.Fields[0].Relation.TargetCollectionID != created.ID {
		t.Fatalf("resolved self relation = %#v, source=%s", created.Fields[0].Relation, created.ID)
	}

	waitForCatalogVersion(t, listenerCache, 1)
	listenerCatalog := listenerCache.Load()
	listenerField := listenerCatalog.Collections["nodes"].Fields[0]
	if listenerField.Relation == nil || listenerField.Relation.TargetCollectionID != created.ID ||
		len(listenerCatalog.Incoming[created.ID]) != 1 {
		t.Fatalf("listener relation catalog = %#v", listenerCatalog)
	}
	serviceCatalog := service.cache.Load()
	if serviceCatalog.Version != 1 || serviceCatalog.Collections["nodes"].Fields[0].Relation.TargetCollectionID != created.ID {
		t.Fatalf("service cache relation catalog = %#v", serviceCatalog)
	}
}

func TestRelationServiceRejectsStalePlanWithoutPartialState(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{
		Name: "owner", Type: TypeRelation,
		Relation: &RelationInput{TargetCollection: "users"},
	}}}
	stalePlan, err := service.PlanCreate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	applyFreshPlan(t, service, CreateCollectionInput{Name: "pages"}, administratorID)

	if _, err := service.ApplyCreate(ctx, input, stalePlan.Checksum, stalePlan.BaseVersion, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyCreate(stale) error = %v, want ErrConflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 2, collections: 2, migrations: 2})
	var physicalTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.posts')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("stale relation apply created table %q", physicalTable)
	}
	var relations int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.field_relations`).Scan(&relations); err != nil {
		t.Fatal(err)
	}
	if relations != 0 {
		t.Fatalf("stale relation apply persisted %d metadata rows", relations)
	}
}

func TestRelationServiceRollsBackDDLAndSchemaCommitWhenRelationMetadataInsertFails(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	if _, err := pool.Exec(ctx, `create function pgbase_meta.reject_relation_metadata()
		returns trigger language plpgsql as $$
		begin
			raise exception using errcode = '23514', message = 'injected relation metadata failure';
		end
		$$;
		create trigger reject_relation_metadata
		before insert on pgbase_meta.field_relations
		for each row execute function pgbase_meta.reject_relation_metadata()`); err != nil {
		t.Fatalf("install metadata failure trigger: %v", err)
	}

	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{
		Name: "owner", Type: TypeRelation,
		Relation: &RelationInput{TargetCollection: "users"},
	}}}
	plan, err := service.PlanCreate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID); !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("ApplyCreate() error = %v, want ErrDataConstraint", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, migrations: 1})
	var physicalTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.posts')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("metadata failure left DDL behind: %q", physicalTable)
	}
	var relations int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.field_relations`).Scan(&relations); err != nil {
		t.Fatal(err)
	}
	if relations != 0 {
		t.Fatalf("metadata failure left %d relation rows", relations)
	}
	if current := service.cache.Load(); current.Version != 1 || len(current.Collections) != 1 {
		t.Fatalf("failed transaction changed cache: %#v", current)
	}
}

func TestRelationServiceAddsUpdatesAndDropsOptionalRelationPreservingFieldIDs(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	posts := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, administratorID)

	add := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "owner", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "users"}},
	}}
	addPlan, err := service.PlanUpdate(ctx, add)
	if err != nil {
		t.Fatalf("PlanUpdate(add): %v", err)
	}
	added, err := service.ApplyUpdate(ctx, add, addPlan.Checksum, addPlan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyUpdate(add): %v", err)
	}
	if added.Fields[0].ID != posts.Fields[0].ID || added.Fields[1].ID == uuid.Nil {
		t.Fatalf("field IDs after add: before=%#v after=%#v", posts.Fields, added.Fields)
	}
	ownerID := added.Fields[1].ID

	change := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "owner", Type: TypeRelation, Unique: true, Relation: &RelationInput{
			TargetCollection: "users", OnDelete: RelationCascade,
		}},
	}}
	changePlan, err := service.PlanUpdate(ctx, change)
	if err != nil {
		t.Fatalf("PlanUpdate(change): %v", err)
	}
	changed, err := service.ApplyUpdate(ctx, change, changePlan.Checksum, changePlan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyUpdate(change): %v", err)
	}
	if changed.Fields[0].ID != posts.Fields[0].ID || changed.Fields[1].ID != ownerID ||
		changed.Fields[1].Relation == nil || changed.Fields[1].Relation.OnDelete != RelationCascade || !changed.Fields[1].Unique {
		t.Fatalf("changed relation did not preserve identity/semantics: %#v", changed.Fields)
	}

	drop := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	dropPlan, err := service.PlanUpdate(ctx, drop)
	if err != nil {
		t.Fatalf("PlanUpdate(drop): %v", err)
	}
	dropped, err := service.ApplyUpdate(ctx, drop, dropPlan.Checksum, dropPlan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyUpdate(drop): %v", err)
	}
	if len(dropped.Fields) != 1 || dropped.Fields[0].ID != posts.Fields[0].ID {
		t.Fatalf("fields after relation drop = %#v", dropped.Fields)
	}
	var ownerColumns, relationRows int
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'owner'`).Scan(&ownerColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.field_relations`).Scan(&relationRows); err != nil {
		t.Fatal(err)
	}
	if ownerColumns != 0 || relationRows != 0 {
		t.Fatalf("drop left owner columns/relation rows = %d/%d", ownerColumns, relationRows)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Incoming) != 0 {
		t.Fatalf("drop left incoming catalog entries: %#v", catalog.Incoming)
	}
}

func TestRelationServiceAddsRequiredRelationOnlyWhenSourceIsEmpty(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	applyFreshPlan(t, service, CreateCollectionInput{
		Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, administratorID)

	requiredOwner := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "owner", Type: TypeRelation, Required: true, Relation: &RelationInput{
			TargetCollection: "users", OnDelete: RelationCascade,
		}},
	}}
	plan, err := service.PlanUpdate(ctx, requiredOwner)
	if err != nil {
		t.Fatalf("PlanUpdate(empty required relation): %v", err)
	}
	updated, err := service.ApplyUpdate(ctx, requiredOwner, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("ApplyUpdate(empty required relation): %v", err)
	}
	if len(updated.Fields) != 2 || updated.Fields[1].Relation == nil || !updated.Fields[1].Required {
		t.Fatalf("required relation update = %#v", updated.Fields)
	}
	var nullable string
	if err := pool.QueryRow(ctx, `select is_nullable from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'owner'`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if nullable != "NO" {
		t.Fatalf("required owner nullable = %q, want NO", nullable)
	}
	applyFreshPlan(t, service, CreateCollectionInput{
		Name: "groups", Fields: []FieldInput{{Name: "name", Type: TypeText}},
	}, administratorID)
	maximumMembers := 4
	requiredMembers := UpdateCollectionInput{Name: "groups", Fields: []FieldInput{
		{Name: "name", Type: TypeText},
		{Name: "members", Type: TypeRelation, Required: true, Relation: &RelationInput{
			TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: &maximumMembers,
		}},
	}}
	membersPlan, err := service.PlanUpdate(ctx, requiredMembers)
	if err != nil {
		t.Fatalf("PlanUpdate(empty required multiple relation): %v", err)
	}
	if _, err := service.ApplyUpdate(ctx, requiredMembers, membersPlan.Checksum, membersPlan.BaseVersion, administratorID); err != nil {
		t.Fatalf("ApplyUpdate(empty required multiple relation): %v", err)
	}
	membersNames := NewRelationPhysicalNames("groups", "members")
	var membersTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass($1)::text, '')`,
		"pgbase_relation."+membersNames.MultiTable).Scan(&membersTable); err != nil {
		t.Fatal(err)
	}
	if membersTable == "" {
		t.Fatal("required multiple relation edge table is missing")
	}

	applyFreshPlan(t, service, CreateCollectionInput{
		Name: "drafts", Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, administratorID)
	maximumReviewers := 4
	requiredReviewers := UpdateCollectionInput{Name: "drafts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "reviewers", Type: TypeRelation, Required: true, Relation: &RelationInput{
			TargetCollection: "users", Cardinality: RelationMultiple, MaxSelect: &maximumReviewers,
		}},
	}}
	stalePlan, err := service.PlanUpdate(ctx, requiredReviewers)
	if err != nil {
		t.Fatalf("PlanUpdate(empty required multiple relation): %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into app.drafts (id, created_at, updated_at, title)
		values ($1, current_timestamp, current_timestamp, 'needs backfill')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	assertBackfillNeeded := func(err error) {
		t.Helper()
		var validationError *ValidationError
		want := []ValidationDetail{{Field: "fields.1.required", Code: "relation_required_backfill_needed"}}
		if !errors.As(err, &validationError) || !reflect.DeepEqual(validationError.Details, want) {
			t.Fatalf("required relation error = %#v, want %#v", err, want)
		}
	}
	_, err = service.PlanUpdate(ctx, requiredReviewers)
	assertBackfillNeeded(err)
	_, err = service.ApplyUpdate(ctx, requiredReviewers, stalePlan.Checksum, stalePlan.BaseVersion, administratorID)
	assertBackfillNeeded(err)

	names := NewRelationPhysicalNames("drafts", "reviewers")
	var edgeTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass($1)::text, '')`,
		"pgbase_relation."+names.MultiTable).Scan(&edgeTable); err != nil {
		t.Fatal(err)
	}
	if edgeTable != "" {
		t.Fatalf("rejected required relation left edge table %q", edgeTable)
	}
}

func TestRelationServiceGuardsIncomingCollectionDeleteUntilRelationRemoved(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{
		Name: "owner", Type: TypeRelation,
		Relation: &RelationInput{TargetCollection: "users"},
	}}}, administratorID)

	assertCollectionReferenced := func(err error) {
		t.Helper()
		var validationError *ValidationError
		if !errors.As(err, &validationError) || len(validationError.Details) != 1 ||
			validationError.Details[0] != (ValidationDetail{Field: "name", Code: "collection_referenced"}) {
			t.Fatalf("error = %#v, want collection_referenced", err)
		}
	}
	_, err := service.PlanDelete(ctx, "users")
	assertCollectionReferenced(err)

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Planning a delete against a catalog that hides the referencing collection
	// produces the same checksum and base version the guard would have refused,
	// so apply has to refuse it a second time on its own pinned catalog.
	forged := Catalog{Version: catalog.Version, Collections: map[string]Collection{"users": catalog.Collections["users"]}}
	forgedPlan, err := (Planner{}).Plan(forged, NewDeleteChange("users"))
	if err != nil {
		t.Fatalf("plan delete against a catalog without the referencing collection: %v", err)
	}
	assertCollectionReferenced(service.ApplyDelete(ctx, "users", forgedPlan.Checksum, forgedPlan.BaseVersion, administratorID))
	assertSchemaCounts(t, pool, schemaCounts{version: 2, collections: 2, fields: 1, migrations: 2})

	remove := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{}}
	removePlan, err := service.PlanUpdate(ctx, remove)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyUpdate(ctx, remove, removePlan.Checksum, removePlan.BaseVersion, administratorID); err != nil {
		t.Fatalf("remove relation: %v", err)
	}
	deletePlan, err := service.PlanDelete(ctx, "users")
	if err != nil {
		t.Fatalf("PlanDelete(after remove): %v", err)
	}
	if err := service.ApplyDelete(ctx, "users", deletePlan.Checksum, deletePlan.BaseVersion, administratorID); err != nil {
		t.Fatalf("ApplyDelete(after remove): %v", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 4, collections: 1, migrations: 4})
	remaining, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := remaining.Collections["users"]; exists || len(remaining.Incoming) != 0 {
		t.Fatalf("catalog after target delete = %#v", remaining)
	}
}
