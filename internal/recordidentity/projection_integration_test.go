//go:build integration

package recordidentity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type projectionDB struct {
	DB
	queries, rowQueries int
}

func TestProjectionOfSharedExpandedMapKeepsUUIDShapedPublicID(t *testing.T) {
	ctx, pool, source := identityFixture(t, "projection_shared_source")
	_, _, target := identityFixtureOnPool(t, ctx, pool, "projection_shared_target")
	source.Fields = []schema.Field{{Name: "targets", Relation: &schema.RelationSpec{TargetCollectionID: target.ID}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{source.Name: source, target.Name: target}}
	service, sourceID, targetID := New(nil), uuid.New(), uuid.New()
	sourcePublic, targetPublic := "Source", strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := service.Reserve(ctx, pool, source, sourceID, &sourcePublic); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, pool, target, targetID, &targetPublic); err != nil {
		t.Fatal(err)
	}
	child := map[string]any{"id": targetID.String()}
	root := map[string]any{"id": sourceID.String(), "expand": map[string]any{"targets": []map[string]any{child, child}}}
	if err := service.Project(ctx, pool, catalog, source, root, root); err != nil {
		t.Fatal(err)
	}
	if root["id"] != sourcePublic || child["id"] != targetPublic {
		t.Fatalf("shared map projected more than once: %#v / %#v", root, child)
	}
}

func (db *projectionDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.queries++
	return db.DB.Query(ctx, sql, args...)
}

func (db *projectionDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	db.rowQueries++
	return db.DB.QueryRow(ctx, sql, args...)
}

func TestProjectionBatchesRootsRelationsAndExpansionsAcrossIdentityScopes(t *testing.T) {
	ctx, pool, source := identityFixture(t, "projection_batch_source")
	_, _, target := identityFixtureOnPool(t, ctx, pool, "projection_batch_target")
	source.Fields = []schema.Field{{Name: "targets", Relation: &schema.RelationSpec{TargetCollectionID: target.ID}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{source.Name: source, target.Name: target}}
	service, internal := New(nil), uuid.New()
	// The same internal UUID in different collections must remain distinct.
	for _, binding := range []struct {
		collection schema.Collection
		public     string
	}{{source, "SourceId"}, {target, "TargetId"}} {
		if _, err := service.Reserve(ctx, pool, binding.collection, internal, &binding.public); err != nil {
			t.Fatal(err)
		}
	}
	records := make([]map[string]any, 250)
	for i := range records {
		records[i] = map[string]any{
			"id": internal.String(), "targets": []any{internal.String(), internal.String()},
			"expand": map[string]any{"targets": []map[string]any{{"id": internal.String()}}},
		}
	}
	db := &projectionDB{DB: pool}
	if err := service.Project(ctx, db, catalog, source, records...); err != nil {
		t.Fatal(err)
	}
	if db.queries != 1 || db.rowQueries != 0 {
		t.Fatalf("projection queries = %d + %d individual lookups", db.queries, db.rowQueries)
	}
	for _, record := range records {
		targets := record["targets"].([]any)
		expanded := record["expand"].(map[string]any)["targets"].([]map[string]any)
		if record["id"] != "SourceId" || targets[0] != "TargetId" || targets[1] != "TargetId" || expanded[0]["id"] != "TargetId" {
			t.Fatalf("incorrect projection: %#v", record)
		}
	}
	if err := service.Project(ctx, db, catalog, source, map[string]any{"id": uuid.NewString()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing mapping = %v", err)
	}
	// Native UUID collections never touch the identity table.
	db = &projectionDB{DB: pool}
	source.RecordID = schema.RecordIDPolicy{Kind: schema.RecordIDUUID}
	source.Fields = nil
	if err := service.Project(ctx, db, catalog, source, map[string]any{"id": internal.String()}); err != nil || db.queries != 0 || db.rowQueries != 0 {
		t.Fatalf("UUID projection queried identities: %d/%d, %v", db.queries, db.rowQueries, err)
	}
}

func TestClosureReverseExpansionProjectsAllIdentitiesOnce(t *testing.T) {
	ctx, pool, parent := identityFixture(t, "reverse_parent")
	_, _, child := identityFixtureOnPool(t, ctx, pool, "reverse_child")
	child.Fields = []schema.Field{{Name: "owner", Relation: &schema.RelationSpec{TargetCollectionID: parent.ID}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{parent.Name: parent, child.Name: child}}
	service, parentID, childID := New(nil), uuid.New(), uuid.New()
	for _, item := range []struct {
		collection schema.Collection
		id         uuid.UUID
		public     string
	}{{parent, parentID, "PersonA"}, {child, childID, "PostA"}} {
		if _, err := service.Reserve(ctx, pool, item.collection, item.id, &item.public); err != nil {
			t.Fatal(err)
		}
	}
	expanded := map[string]any{"id": childID.String(), "owner": parentID.String()}
	root := map[string]any{"id": parentID.String(), "expand": map[string]any{child.Name + "_via_owner": []any{expanded}}}
	db := &projectionDB{DB: pool}
	if err := service.Project(ctx, db, catalog, parent, root); err != nil {
		t.Fatal(err)
	}
	if root["id"] != "PersonA" || expanded["id"] != "PostA" || expanded["owner"] != "PersonA" || db.queries != 1 {
		t.Fatalf("projected=%#v child=%#v queries=%d", root, expanded, db.queries)
	}
}
