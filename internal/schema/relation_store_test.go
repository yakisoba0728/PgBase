package schema

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRelationStoreRoundTripsSingleMultipleSelfAndCycleWithStableIncomingOrder(t *testing.T) {
	ctx, pool, store := newRelationStoreTest(t)
	createdAt := relationStoreDatabaseTime(t, ctx, pool)

	usersID := relationStoreUUID(100)
	tagsID := relationStoreUUID(200)
	commentsID := relationStoreUUID(250)
	postsID := relationStoreUUID(300)
	alphaID := relationStoreUUID(400)
	betaID := relationStoreUUID(500)

	insertRelationStoreCollection(t, ctx, store, relationStoreCollection(usersID, "users"), PlannedCollection{Name: "users", TableName: "users"}, createdAt)
	insertRelationStoreCollection(t, ctx, store, relationStoreCollection(tagsID, "tags"), PlannedCollection{Name: "tags", TableName: "tags"}, createdAt)
	insertRelationStoreCollection(t, ctx, store, relationStoreCollection(alphaID, "alpha"), PlannedCollection{Name: "alpha", TableName: "alpha"}, createdAt)

	ownerFieldID := relationStoreUUID(399)
	reviewerFieldID := relationStoreUUID(301)
	labelsFieldID := relationStoreUUID(302)
	parentFieldID := relationStoreUUID(303)
	slugFieldID := relationStoreUUID(304)
	posts := relationStoreCollection(postsID, "posts",
		relationStoreField(ownerFieldID, "owner", RelationSingle, 1, true, true, 0),
		relationStoreField(reviewerFieldID, "reviewer", RelationSingle, 0, false, false, 1),
		relationStoreField(labelsFieldID, "labels", RelationMultiple, 0, false, false, 2),
		relationStoreField(parentFieldID, "parent", RelationSingle, 0, false, false, 3),
		Field{ID: slugFieldID, Name: "slug", ColumnName: "slug", Type: TypeText, Indexed: true, Ordinal: 4},
	)
	postsDefinition := PlannedCollection{Name: "posts", TableName: "posts", Fields: []PlannedField{
		relationStorePlannedField("owner", RelationSingle, 1, true, true, 0,
			relationStorePlannedRelation(&usersID, "users", RelationSingle, 1, 1, RelationRestrict, RelationStorageInlineFKV1, "r_posts_owner")),
		relationStorePlannedField("reviewer", RelationSingle, 0, false, false, 1,
			relationStorePlannedRelation(&usersID, "users", RelationSingle, 0, 1, RelationSetNull, RelationStorageInlineFKV1, "r_posts_reviewer")),
		relationStorePlannedField("labels", RelationMultiple, 0, false, false, 2,
			relationStorePlannedRelation(&tagsID, "tags", RelationMultiple, 0, 20, RelationCascade, RelationStorageEdgeTableV1, "r_posts_labels")),
		relationStorePlannedField("parent", RelationSingle, 0, false, false, 3,
			relationStorePlannedRelation(nil, "posts", RelationSingle, 0, 1, RelationSetNull, RelationStorageInlineFKV1, "r_posts_parent")),
		{Name: "slug", Type: TypeText, Indexed: true, Ordinal: 4},
	}}
	insertRelationStoreCollection(t, ctx, store, posts, postsDefinition, createdAt)

	commentAuthorFieldID := relationStoreUUID(251)
	comments := relationStoreCollection(commentsID, "comments",
		relationStoreField(commentAuthorFieldID, "author", RelationSingle, 1, true, false, 0),
	)
	commentsDefinition := PlannedCollection{Name: "comments", TableName: "comments", Fields: []PlannedField{
		relationStorePlannedField("author", RelationSingle, 1, true, false, 0,
			relationStorePlannedRelation(&usersID, "users", RelationSingle, 1, 1, RelationRestrict, RelationStorageInlineFKV1, "r_comments_author")),
	}}
	insertRelationStoreCollection(t, ctx, store, comments, commentsDefinition, createdAt)

	betaToAlphaFieldID := relationStoreUUID(501)
	beta := relationStoreCollection(betaID, "beta",
		relationStoreField(betaToAlphaFieldID, "alpha", RelationSingle, 0, false, false, 0),
	)
	betaDefinition := PlannedCollection{Name: "beta", TableName: "beta", Fields: []PlannedField{
		relationStorePlannedField("alpha", RelationSingle, 0, false, false, 0,
			relationStorePlannedRelation(&alphaID, "alpha", RelationSingle, 0, 1, RelationRestrict, RelationStorageInlineFKV1, "r_beta_alpha")),
	}}
	insertRelationStoreCollection(t, ctx, store, beta, betaDefinition, createdAt)

	alphaToBetaFieldID := relationStoreUUID(401)
	alphaUpdated := relationStoreCollection(alphaID, "alpha",
		relationStoreField(alphaToBetaFieldID, "beta", RelationSingle, 0, false, false, 0),
	)
	alphaDefinition := PlannedCollection{Name: "alpha", TableName: "alpha", Fields: []PlannedField{
		relationStorePlannedField("beta", RelationSingle, 0, false, false, 0,
			relationStorePlannedRelation(&betaID, "beta", RelationSingle, 0, 1, RelationRestrict, RelationStorageInlineFKV1, "r_alpha_beta")),
	}}
	if err := store.UpdateCollection(ctx, alphaUpdated, alphaDefinition, createdAt); err != nil {
		t.Fatalf("update alpha cycle metadata: %v", err)
	}

	catalog, err := store.ReadCatalog(ctx, 41)
	if err != nil {
		t.Fatalf("read relation catalog: %v", err)
	}
	if catalog.Version != 41 || len(catalog.Collections) != 6 {
		t.Fatalf("catalog version/collections=%d/%d want=41/6", catalog.Version, len(catalog.Collections))
	}
	assertRelationStoreSpec(t, relationStoreCatalogField(t, catalog, "posts", "owner"), usersID, RelationSingle, "r_posts_owner")
	assertRelationStoreSpec(t, relationStoreCatalogField(t, catalog, "posts", "labels"), tagsID, RelationMultiple, "r_posts_labels")
	assertRelationStoreSpec(t, relationStoreCatalogField(t, catalog, "posts", "parent"), postsID, RelationSingle, "r_posts_parent")
	assertRelationStoreSpec(t, relationStoreCatalogField(t, catalog, "alpha", "beta"), betaID, RelationSingle, "r_alpha_beta")
	assertRelationStoreSpec(t, relationStoreCatalogField(t, catalog, "beta", "alpha"), alphaID, RelationSingle, "r_beta_alpha")

	userIncoming := catalog.Incoming[usersID]
	wantUserIncoming := []struct {
		source uuid.UUID
		field  uuid.UUID
	}{
		{commentsID, commentAuthorFieldID},
		{postsID, reviewerFieldID},
		{postsID, ownerFieldID},
	}
	if len(userIncoming) != len(wantUserIncoming) {
		t.Fatalf("users incoming=%+v want %d refs", userIncoming, len(wantUserIncoming))
	}
	for index, want := range wantUserIncoming {
		if userIncoming[index].SourceCollectionID != want.source || userIncoming[index].SourceFieldID != want.field {
			t.Fatalf("users incoming[%d]=%+v want source=%s field=%s", index, userIncoming[index], want.source, want.field)
		}
	}
	if got := catalog.Incoming[postsID]; len(got) != 1 || got[0].SourceFieldID != parentFieldID {
		t.Fatalf("self incoming=%+v", got)
	}
	if got := catalog.Incoming[alphaID]; len(got) != 1 || got[0].SourceCollectionID != betaID {
		t.Fatalf("cycle incoming for alpha=%+v", got)
	}
	if got := catalog.Incoming[betaID]; len(got) != 1 || got[0].SourceCollectionID != alphaID {
		t.Fatalf("cycle incoming for beta=%+v", got)
	}

	var scalarIndexes, relationIndexes int
	if err := pool.QueryRow(ctx, `select
		count(*) filter (where field_id = $1),
		count(*) filter (where field_id in ($2, $3, $4, $5))
		from pgbase_meta.collection_indexes`,
		slugFieldID, ownerFieldID, reviewerFieldID, labelsFieldID, parentFieldID,
	).Scan(&scalarIndexes, &relationIndexes); err != nil {
		t.Fatal(err)
	}
	if scalarIndexes != 1 || relationIndexes != 0 {
		t.Fatalf("collection index metadata scalar/relation=%d/%d want=1/0", scalarIndexes, relationIndexes)
	}
	var relationTimestamps int
	if err := pool.QueryRow(ctx,
		`select count(*) from pgbase_meta.field_relations where created_at = $1`, createdAt,
	).Scan(&relationTimestamps); err != nil {
		t.Fatal(err)
	}
	if relationTimestamps != 7 {
		t.Fatalf("relation rows using database timestamp=%d want=7", relationTimestamps)
	}
}

func TestRelationStoreRejectsCorruptCatalogWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, context.Context, *database.Pool, relationStoreFixture)
	}{
		{
			name: "unknown field",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `alter table pgbase_meta.field_relations
					drop constraint field_relations_source_field_fk`)
				relationStoreExec(t, ctx, pool, `update pgbase_meta.field_relations
					set field_id = $1 where field_id = $2`, relationStoreUUID(999), fixture.relationFieldID)
			},
		},
		{
			name: "unknown source",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `alter table pgbase_meta.field_relations
					drop constraint field_relations_source_field_fk`)
				relationStoreExec(t, ctx, pool, `update pgbase_meta.field_relations
					set source_collection_id = $1 where field_id = $2`, relationStoreUUID(998), fixture.relationFieldID)
			},
		},
		{
			name: "source mismatch",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `alter table pgbase_meta.field_relations
					drop constraint field_relations_source_field_fk`)
				relationStoreExec(t, ctx, pool, `update pgbase_meta.field_relations
					set source_collection_id = $1 where field_id = $2`, fixture.otherSourceID, fixture.relationFieldID)
			},
		},
		{
			name: "unknown target",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `alter table pgbase_meta.field_relations
					drop constraint field_relations_target_collection_id_fkey`)
				relationStoreExec(t, ctx, pool, `update pgbase_meta.field_relations
					set target_collection_id = $1 where field_id = $2`, relationStoreUUID(997), fixture.relationFieldID)
			},
		},
		{
			name: "scalar field relation metadata",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `insert into pgbase_meta.field_relations
					(field_id, source_collection_id, target_collection_id, cardinality,
					 min_select, max_select, on_delete, value_codec_version, storage_kind,
					 storage_name, created_at)
					values ($1, $2, $3, 'single', 0, 1, 'restrict', 1, 'inline_fk_v1',
					 'secret_scalar_storage', current_timestamp)`,
					fixture.scalarFieldID, fixture.sourceID, fixture.targetID)
			},
		},
		{
			name: "missing relation metadata",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool,
					`delete from pgbase_meta.field_relations where field_id = $1`, fixture.relationFieldID)
			},
		},
		{
			name: "invalid relation spec",
			corrupt: func(t *testing.T, ctx context.Context, pool *database.Pool, fixture relationStoreFixture) {
				relationStoreExec(t, ctx, pool, `alter table pgbase_meta.field_relations
					drop constraint field_relations_value_codec_version_check`)
				relationStoreExec(t, ctx, pool, `update pgbase_meta.field_relations
					set value_codec_version = 2, storage_name = 'secret_invalid_storage'
					where field_id = $1`, fixture.relationFieldID)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, pool, store := newRelationStoreTest(t)
			fixture := seedRelationStoreFixture(t, ctx, pool, store)
			testCase.corrupt(t, ctx, pool, fixture)

			_, err := store.ReadCatalog(ctx, 1)
			if !errors.Is(err, errCatalogIntegrity) {
				t.Fatalf("ReadCatalog error=%v want catalog integrity error", err)
			}
			if testCase.name == "invalid relation spec" {
				var validationError *ValidationError
				if !errors.As(err, &validationError) {
					t.Fatalf("invalid persisted spec did not preserve ValidationError: %v", err)
				}
			}
			for _, secret := range []string{
				fixture.relationFieldID.String(),
				fixture.sourceID.String(),
				fixture.targetID.String(),
				"secret_scalar_storage",
				"secret_invalid_storage",
				"select ",
				"update ",
			} {
				if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secret)) {
					t.Fatalf("catalog integrity error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestRelationStoreUpdatePreservesFieldIDDropsAndAtomicallyReinsertsMetadata(t *testing.T) {
	ctx, pool, store := newRelationStoreTest(t)
	fixture := seedRelationStoreFixture(t, ctx, pool, store)
	secondTargetID := relationStoreUUID(720)
	createdAt := relationStoreDatabaseTime(t, ctx, pool)
	insertRelationStoreCollection(t, ctx, store,
		relationStoreCollection(secondTargetID, "second_target"),
		PlannedCollection{Name: "second_target", TableName: "second_target"},
		createdAt,
	)

	droppedFieldID := relationStoreUUID(711)
	originalSource := relationStoreCollection(fixture.sourceID, "source",
		relationStoreField(fixture.relationFieldID, "target", RelationSingle, 0, false, false, 0),
		relationStoreField(droppedFieldID, "dropped", RelationSingle, 0, false, false, 1),
		Field{ID: fixture.scalarFieldID, Name: "title", ColumnName: "title", Type: TypeText, Indexed: true, Ordinal: 2},
	)
	originalDefinition := PlannedCollection{Name: "source", TableName: "source", Fields: []PlannedField{
		relationStorePlannedField("target", RelationSingle, 0, false, false, 0,
			relationStorePlannedRelation(&fixture.targetID, "target", RelationSingle, 0, 1, RelationRestrict, RelationStorageInlineFKV1, "r_source_target")),
		relationStorePlannedField("dropped", RelationSingle, 0, false, false, 1,
			relationStorePlannedRelation(&fixture.targetID, "target", RelationSingle, 0, 1, RelationRestrict, RelationStorageInlineFKV1, "r_source_dropped")),
		{Name: "title", Type: TypeText, Indexed: true, Ordinal: 2},
	}}
	if err := store.UpdateCollection(ctx, originalSource, originalDefinition, createdAt); err != nil {
		t.Fatalf("extend original source: %v", err)
	}

	updatedAt := relationStoreDatabaseTime(t, ctx, pool)
	updatedSource := relationStoreCollection(fixture.sourceID, "source",
		relationStoreField(fixture.relationFieldID, "target", RelationMultiple, 0, false, false, 0),
		Field{ID: fixture.scalarFieldID, Name: "title", ColumnName: "title", Type: TypeText, Indexed: true, Ordinal: 1},
	)
	updatedDefinition := PlannedCollection{Name: "source", TableName: "source", Fields: []PlannedField{
		relationStorePlannedField("target", RelationMultiple, 0, false, false, 0,
			relationStorePlannedRelation(&secondTargetID, "second_target", RelationMultiple, 0, 30, RelationCascade, RelationStorageEdgeTableV1, "r_source_target_v2")),
		{Name: "title", Type: TypeText, Indexed: true, Ordinal: 1},
	}}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return NewStore(tx).UpdateCollection(ctx, updatedSource, updatedDefinition, updatedAt)
	}); err != nil {
		t.Fatalf("update relation metadata: %v", err)
	}

	catalog, err := store.ReadCatalog(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	field := relationStoreCatalogField(t, catalog, "source", "target")
	if field.ID != fixture.relationFieldID {
		t.Fatalf("updated relation field ID=%s want=%s", field.ID, fixture.relationFieldID)
	}
	assertRelationStoreSpec(t, field, secondTargetID, RelationMultiple, "r_source_target_v2")
	if _, found := relationStoreFindField(catalog.Collections["source"], "dropped"); found {
		t.Fatal("dropped relation field remains in catalog")
	}
	var relationRows, droppedRows int
	if err := pool.QueryRow(ctx, `select
		count(*) filter (where field_id = $1 and created_at = $3),
		count(*) filter (where field_id = $2)
		from pgbase_meta.field_relations`, fixture.relationFieldID, droppedFieldID, updatedAt,
	).Scan(&relationRows, &droppedRows); err != nil {
		t.Fatal(err)
	}
	if relationRows != 1 || droppedRows != 0 {
		t.Fatalf("updated/dropped relation rows=%d/%d want=1/0", relationRows, droppedRows)
	}

	missingTargetID := relationStoreUUID(799)
	brokenDefinition := updatedDefinition
	brokenRelation := *brokenDefinition.Fields[0].Relation
	brokenRelation.TargetCollectionID = &missingTargetID
	brokenRelation.TargetCollection = "missing_target"
	brokenRelation.Storage.Name = "r_source_target_broken"
	brokenDefinition.Fields = append([]PlannedField(nil), brokenDefinition.Fields...)
	brokenDefinition.Fields[0].Relation = &brokenRelation
	brokenAt := relationStoreDatabaseTime(t, ctx, pool)
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return NewStore(tx).UpdateCollection(ctx, updatedSource, brokenDefinition, brokenAt)
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23503" {
		t.Fatalf("broken update error=%v SQLSTATE=%q want=23503", err, relationStorePostgresCode(postgresError))
	}

	catalog, err = store.ReadCatalog(ctx, 2)
	if err != nil {
		t.Fatalf("read catalog after rolled-back update: %v", err)
	}
	field = relationStoreCatalogField(t, catalog, "source", "target")
	assertRelationStoreSpec(t, field, secondTargetID, RelationMultiple, "r_source_target_v2")
}

func TestRelationStoreTargetDeleteIsRestrictedUntilSourceIsDeleted(t *testing.T) {
	ctx, pool, store := newRelationStoreTest(t)
	fixture := seedRelationStoreFixture(t, ctx, pool, store)

	err := store.DeleteCollection(ctx, fixture.targetID)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || (postgresError.Code != "23001" && postgresError.Code != "23503") {
		t.Fatalf("target delete error=%v SQLSTATE=%q want=23001 (PostgreSQL 18) or 23503 (PostgreSQL 16/17)",
			err, relationStorePostgresCode(postgresError))
	}
	if err := store.DeleteCollection(ctx, fixture.sourceID); err != nil {
		t.Fatalf("delete source collection: %v", err)
	}
	if err := store.DeleteCollection(ctx, fixture.targetID); err != nil {
		t.Fatalf("delete unreferenced target collection: %v", err)
	}

	var collections, relations int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from pgbase_meta.collections),
		(select count(*) from pgbase_meta.field_relations)`).Scan(&collections, &relations); err != nil {
		t.Fatal(err)
	}
	if collections != 1 || relations != 0 {
		t.Fatalf("remaining collections/relations=%d/%d want=1/0", collections, relations)
	}
}

type relationStoreFixture struct {
	sourceID        uuid.UUID
	otherSourceID   uuid.UUID
	targetID        uuid.UUID
	relationFieldID uuid.UUID
	scalarFieldID   uuid.UUID
}

func newRelationStoreTest(t *testing.T) (context.Context, *database.Pool, *Store) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatalf("apply system migrations: %v", err)
	}
	return ctx, pool, NewStore(pool)
}

func seedRelationStoreFixture(t *testing.T, ctx context.Context, pool *database.Pool, store *Store) relationStoreFixture {
	t.Helper()
	fixture := relationStoreFixture{
		sourceID:        relationStoreUUID(700),
		otherSourceID:   relationStoreUUID(701),
		targetID:        relationStoreUUID(702),
		relationFieldID: relationStoreUUID(710),
		scalarFieldID:   relationStoreUUID(712),
	}
	createdAt := relationStoreDatabaseTime(t, ctx, pool)
	insertRelationStoreCollection(t, ctx, store,
		relationStoreCollection(fixture.targetID, "target"),
		PlannedCollection{Name: "target", TableName: "target"},
		createdAt,
	)
	insertRelationStoreCollection(t, ctx, store,
		relationStoreCollection(fixture.otherSourceID, "other_source"),
		PlannedCollection{Name: "other_source", TableName: "other_source"},
		createdAt,
	)
	source := relationStoreCollection(fixture.sourceID, "source",
		relationStoreField(fixture.relationFieldID, "target", RelationSingle, 0, false, false, 0),
		Field{ID: fixture.scalarFieldID, Name: "title", ColumnName: "title", Type: TypeText, Indexed: true, Ordinal: 1},
	)
	definition := PlannedCollection{Name: "source", TableName: "source", Fields: []PlannedField{
		relationStorePlannedField("target", RelationSingle, 0, false, false, 0,
			relationStorePlannedRelation(&fixture.targetID, "target", RelationSingle, 0, 1, RelationRestrict, RelationStorageInlineFKV1, "r_source_target")),
		{Name: "title", Type: TypeText, Indexed: true, Ordinal: 1},
	}}
	insertRelationStoreCollection(t, ctx, store, source, definition, createdAt)
	return fixture
}

func relationStoreCollection(id uuid.UUID, name string, fields ...Field) Collection {
	return Collection{ID: id, Name: name, TableName: name, Fields: fields}
}

func relationStoreField(
	id uuid.UUID,
	name string,
	cardinality RelationCardinality,
	minimum int,
	required bool,
	unique bool,
	ordinal int,
) Field {
	return Field{
		ID: id, Name: name, ColumnName: name, Type: TypeRelation,
		Required: required, Unique: unique, Indexed: true, Ordinal: ordinal,
		Relation: &RelationSpec{Cardinality: cardinality, MinSelect: minimum},
	}
}

func relationStorePlannedField(
	name string,
	cardinality RelationCardinality,
	minimum int,
	required bool,
	unique bool,
	ordinal int,
	relation *PlannedRelation,
) PlannedField {
	return PlannedField{
		Name: name, Type: TypeRelation, Required: required, Unique: unique, Indexed: true,
		Relation: relation, Ordinal: ordinal,
	}
}

func relationStorePlannedRelation(
	targetID *uuid.UUID,
	targetName string,
	cardinality RelationCardinality,
	minimum int,
	maximum int,
	onDelete RelationDeleteAction,
	storageKind RelationStorageKind,
	storageName string,
) *PlannedRelation {
	return &PlannedRelation{
		TargetCollection: targetName, TargetCollectionID: targetID,
		Cardinality: cardinality, MinSelect: minimum, MaxSelect: maximum,
		OnDelete: onDelete, ValueCodecVersion: RelationValueCodecVersion,
		Storage: RelationStorage{Kind: storageKind, Name: storageName},
	}
}

func insertRelationStoreCollection(
	t *testing.T,
	ctx context.Context,
	store *Store,
	collection Collection,
	definition PlannedCollection,
	createdAt time.Time,
) {
	t.Helper()
	if err := store.InsertCollection(ctx, collection, definition, createdAt); err != nil {
		t.Fatalf("insert collection %q: %v", collection.Name, err)
	}
}

func relationStoreDatabaseTime(t *testing.T, ctx context.Context, pool *database.Pool) time.Time {
	t.Helper()
	var timestamp time.Time
	if err := pool.QueryRow(ctx, `select transaction_timestamp()`).Scan(&timestamp); err != nil {
		t.Fatal(err)
	}
	return timestamp.UTC()
}

func relationStoreCatalogField(t *testing.T, catalog Catalog, collectionName, fieldName string) Field {
	t.Helper()
	collection, ok := catalog.Collections[collectionName]
	if !ok {
		t.Fatalf("collection %q not found", collectionName)
	}
	field, ok := relationStoreFindField(collection, fieldName)
	if !ok {
		t.Fatalf("field %q.%q not found", collectionName, fieldName)
	}
	return field
}

func relationStoreFindField(collection Collection, name string) (Field, bool) {
	for _, field := range collection.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

func assertRelationStoreSpec(
	t *testing.T,
	field Field,
	targetCollectionID uuid.UUID,
	cardinality RelationCardinality,
	storageName string,
) {
	t.Helper()
	if field.Relation == nil {
		t.Fatalf("field %q has nil relation", field.Name)
	}
	if field.Relation.TargetCollectionID != targetCollectionID ||
		field.Relation.Cardinality != cardinality ||
		field.Relation.Storage.Name != storageName {
		t.Fatalf("field %q relation=%+v want target=%s cardinality=%s storage=%q",
			field.Name, field.Relation, targetCollectionID, cardinality, storageName)
	}
}

func relationStoreExec(t *testing.T, ctx context.Context, pool *database.Pool, statement string, arguments ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, arguments...); err != nil {
		t.Fatalf("corrupt relation fixture: %v", err)
	}
}

func relationStoreUUID(suffix int) uuid.UUID {
	return uuid.MustParse("019551d0-e9c0-7000-8000-" + relationStoreLeftPad(suffix, 12))
}

func relationStoreLeftPad(value int, width int) string {
	decimal := strings.Repeat("0", width) + strconv.Itoa(value)
	return decimal[len(decimal)-width:]
}

func relationStorePostgresCode(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.Code
}
