//go:build integration

package schema

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCollectionRenameKeepsIdentityAndRenamesOwnedObjects(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	created := applyChange(t, service, NewCreateChange(CreateCollectionInput{
		Name:   "rename_source",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}, {Name: "score", Type: TypeBigInt}},
		Indexes: []IndexInput{
			{Name: "title_lower", Keys: []IndexKeyInput{{Expression: "lower(title)", Order: "desc"}}, Where: "title is not null"},
			{Name: "score_raw", RawSQL: "CREATE INDEX score_raw ON rename_source ((abs(score))) WHERE score IS NOT NULL"},
		},
	}), admin).Collection
	rules := `{"list":{"op":"eq","field":"@collection.rename_source.title","value":"@collection.rename_source.title"},"view":{"op":"allow"},"create":{"op":"allow"},"update":{"op":"allow"},"delete":{"op":"allow"}}`
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb) on conflict(collection_id) do update set rules=excluded.rules`, created.ID, rules); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.auth_collections set list_rule='@collection.rename_source.title = ''visible''' where name='users'`); err != nil {
		t.Fatal(err)
	}

	change := NewUpdateChange(UpdateCollectionInput{Name: "rename_target", CollectionID: &created.ID})
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Statements, "\n")
	if !strings.Contains(joined, `ALTER TABLE "app"."rename_source" RENAME TO "rename_target"`) || !strings.Contains(joined, "ALTER INDEX") {
		t.Fatalf("rename plan did not cover table and indexes:\n%s", joined)
	}
	result, err := service.Apply(context.Background(), LegacyApply(change, plan, admin))
	if err != nil {
		t.Fatal(err)
	}
	if result.Collection.ID != created.ID || result.Collection.Name != "rename_target" || result.Collection.TableName != "rename_target" {
		t.Fatalf("renamed collection = %#v", result.Collection)
	}
	var oldTable, newTable string
	if err := pool.QueryRow(context.Background(), `select coalesce(to_regclass('app.rename_source')::text,''),coalesce(to_regclass('app.rename_target')::text,'')`).Scan(&oldTable, &newTable); err != nil {
		t.Fatal(err)
	}
	if oldTable != "" || newTable != "app.rename_target" {
		t.Fatalf("physical tables old=%q new=%q", oldTable, newTable)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := catalog.Collections["rename_source"]; stale || catalog.Collections["rename_target"].ID != created.ID {
		t.Fatalf("catalog after rename = %#v", catalog.Collections)
	}
	var storedRules []byte
	var authRule string
	if err := pool.QueryRow(context.Background(), `select rules from pgbase_meta.collection_rules where collection_id=$1`, created.ID).Scan(&storedRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `select list_rule from pgbase_meta.auth_collections where name='users'`).Scan(&authRule); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(storedRules, []byte(`"field": "@collection.rename_target.title"`)) || !bytes.Contains(storedRules, []byte(`"value": "@collection.rename_source.title"`)) || !strings.Contains(authRule, "@collection.rename_target.title") {
		t.Fatalf("renamed rules collection=%s auth=%q", storedRules, authRule)
	}
	if err := PreflightCatalog(context.Background(), pool, &Cache{}); err != nil {
		t.Fatalf("renamed physical schema drifted: %v", err)
	}
	document, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := documentCollection(document, "rename_target")
	if !ok || len(item.Indexes) != 2 {
		t.Fatalf("advanced index export = %#v", item.Indexes)
	}
	rawIndex := item.Indexes[0]
	if rawIndex.Name != "score_raw" {
		rawIndex = item.Indexes[1]
	}
	if !strings.Contains(rawIndex.RawSQL, "rename_target") {
		t.Fatalf("advanced index export = %#v", item.Indexes)
	}
}

func TestCollectionRenameRebuildsRelationObjectsAndUpdatesViewSource(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_relation_target"}), admin)
	created := applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_relation_source", Fields: []FieldInput{
		{Name: "label", Type: TypeText},
		{Name: "targets", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "rename_relation_target", Cardinality: RelationMultiple, MaxSelect: intPointer(10)}},
		{Name: "children", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "rename_relation_source", Cardinality: RelationMultiple, MaxSelect: intPointer(10)}},
	}}), admin).Collection
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_relation_view", Type: CollectionTypeView, View: &ViewInput{Source: created.Name, Projection: []string{"label"}}}), admin)
	oldEdge := created.Fields[1].Relation.Storage.Name

	change := NewUpdateChange(UpdateCollectionInput{Name: "renamed_relation_source", CollectionID: &created.ID})
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), LegacyApply(change, plan, admin))
	if err != nil {
		t.Fatal(err)
	}
	newEdge := result.Collection.Fields[1].Relation.Storage.Name
	if oldEdge == newEdge {
		t.Fatalf("relation storage was not renamed: %q", oldEdge)
	}
	var oldTable, newTable string
	if err := pool.QueryRow(context.Background(), `select coalesce(to_regclass($1)::text,''),coalesce(to_regclass($2)::text,'')`, "pgbase_relation."+oldEdge, "pgbase_relation."+newEdge).Scan(&oldTable, &newTable); err != nil {
		t.Fatal(err)
	}
	if oldTable != "" || newTable == "" {
		t.Fatalf("relation tables old=%q new=%q", oldTable, newTable)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view := catalog.Collections["rename_relation_view"]
	if view.View == nil || view.View.Source != "renamed_relation_source" || view.View.SourceCollectionID != created.ID {
		t.Fatalf("dependent view = %#v", view.View)
	}
	if err := PreflightCatalog(context.Background(), pool, &Cache{}); err != nil {
		t.Fatal(err)
	}
}
