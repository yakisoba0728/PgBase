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
			{Name: "title_lower", Keys: []IndexKeyInput{{Expression: "lower(title)", Collation: "C", Order: "desc"}}, Where: "title is not null"},
			{Name: "score_raw", RawSQL: "CREATE INDEX score_raw ON rename_source ((abs(score))) WHERE score IS NOT NULL"},
		},
	}), admin).Collection
	rules := `{"list":{"op":"eq","field":"@collection.rename_source.title","value":"@collection.rename_source.title"},"view":{"op":"allow"},"create":{"op":"allow"},"update":{"op":"allow"},"delete":{"op":"allow"}}`
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb) on conflict(collection_id) do update set rules=excluded.rules`, created.ID, rules); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.auth_collections set list_rule='@collection.rename_source.title = ''@collection.rename_source.literal''' where name='users'`); err != nil {
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
	indexDefinitions := map[string]string{}
	for _, index := range catalog.Collections["rename_target"].Indexes {
		var definition string
		if err := pool.QueryRow(context.Background(), `select pg_get_indexdef(to_regclass($1))`, "app."+index.IndexName).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		indexDefinitions[index.Name] = definition
	}
	if !strings.Contains(indexDefinitions["title_lower"], `lower(title) COLLATE "C" DESC`) || !strings.Contains(indexDefinitions["title_lower"], "WHERE (title IS NOT NULL)") || !strings.Contains(indexDefinitions["score_raw"], "abs(score)") {
		t.Fatalf("physical advanced indexes = %#v", indexDefinitions)
	}
	var storedRules []byte
	var authRule string
	if err := pool.QueryRow(context.Background(), `select rules from pgbase_meta.collection_rules where collection_id=$1`, created.ID).Scan(&storedRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `select list_rule from pgbase_meta.auth_collections where name='users'`).Scan(&authRule); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(storedRules, []byte(`"field": "@collection.rename_target.title"`)) || !bytes.Contains(storedRules, []byte(`"value": "@collection.rename_source.title"`)) || !strings.Contains(authRule, "@collection.rename_target.title") || !strings.Contains(authRule, "'@collection.rename_source.literal'") {
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

func TestCollectionRenamePreservesSQLViewOIDBindingAndRefreshesExport(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	ctx := context.Background()
	source := applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_sql_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}), admin).Collection
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_sql_view", Type: CollectionTypeView,
		View: &ViewInput{SQL: `select id,title from app.rename_sql_source`}}), admin)
	if _, err := pool.Exec(ctx, `insert into app.rename_sql_source(id,created_at,updated_at,title) values(gen_random_uuid(),now(),now(),'bound')`); err != nil {
		t.Fatal(err)
	}

	change := NewUpdateChange(UpdateCollectionInput{Name: "renamed_sql_source", CollectionID: &source.ID})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, LegacyApply(change, plan, admin)); err != nil {
		t.Fatal(err)
	}
	var title, storedSQL, physicalSQL string
	if err := pool.QueryRow(ctx, `select title from app.rename_sql_view`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select views.sql,pg_get_viewdef('app.rename_sql_view'::regclass,true)
		from pgbase_meta.collection_views views join pgbase_meta.collections collections on collections.id=views.collection_id
		where collections.name='rename_sql_view'`).Scan(&storedSQL, &physicalSQL); err != nil {
		t.Fatal(err)
	}
	if title != "bound" || storedSQL != physicalSQL || !strings.Contains(storedSQL, "renamed_sql_source") || strings.Contains(storedSQL, "rename_sql_source") {
		t.Fatalf("title=%q stored=%q physical=%q", title, storedSQL, physicalSQL)
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exported, ok := documentCollection(document, "rename_sql_view")
	if !ok || exported.View == nil || !strings.Contains(exported.View.SQL, "renamed_sql_source") {
		t.Fatalf("SQL view export = %#v", exported)
	}
}

func TestViewRenamePreservesInheritedTextRecordIDPolicy(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	ctx := context.Background()
	policy := RecordIDPolicy{Kind: RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`, AutogeneratePattern: `[A-Z]{8}`}
	source := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "text_view_source", RecordID: &policy, Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, admin)
	view := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "text_view", View: &ViewInput{Source: source.Name, Projection: []string{"title"}},
	}, admin)
	if view.RecordID != policy {
		t.Fatalf("created view record ID policy = %#v, want %#v", view.RecordID, policy)
	}
	change := NewUpdateChange(UpdateCollectionInput{Name: "renamed_text_view", CollectionID: &view.ID})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Definition.RecordID != policy {
		t.Fatalf("rename preview record ID policy = %#v, want %#v", plan.Definition.RecordID, policy)
	}
	result, err := service.Apply(ctx, LegacyApply(change, plan, admin))
	if err != nil {
		t.Fatal(err)
	}
	if result.Collection.ID != view.ID || result.Collection.RecordID != policy {
		t.Fatalf("renamed view identity = %#v", result.Collection)
	}
	reloaded := NewService(pool, &Cache{}, nil)
	catalog, err := reloaded.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Collections["renamed_text_view"].RecordID != policy || catalog.Collections[source.Name].RecordID != policy {
		t.Fatalf("reloaded record ID policies = %#v", catalog.Collections)
	}
	document, err := reloaded.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{source.Name, "renamed_text_view"} {
		item, ok := documentCollection(document, name)
		if !ok || item.RecordID == nil || *item.RecordID != policy {
			t.Fatalf("exported %s record ID policy = %#v, want %#v", name, item.RecordID, policy)
		}
	}
}

func TestCollectionRenameRollsBackTableAndSQLViewRefreshOnMetadataFailure(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	ctx := context.Background()
	source := applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_rollback_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}), admin).Collection
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "rename_rollback_view", Type: CollectionTypeView,
		View: &ViewInput{SQL: `select id,title from app.rename_rollback_source`}}), admin)
	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_rename_metadata() returns trigger language plpgsql as $$
		begin raise exception 'injected rename metadata failure'; end $$;
		create trigger fail_rename_metadata before update of name on pgbase_meta.collections
		for each row when (old.id='`+source.ID.String()+`'::uuid) execute function pgbase_meta.fail_rename_metadata()`); err != nil {
		t.Fatal(err)
	}
	change := NewUpdateChange(UpdateCollectionInput{Name: "renamed_rollback_source", CollectionID: &source.ID})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, LegacyApply(change, plan, admin)); err == nil {
		t.Fatal("rename succeeded through injected metadata failure")
	}
	var oldTable, newTable, storedSQL, physicalSQL string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.rename_rollback_source')::text,''),coalesce(to_regclass('app.renamed_rollback_source')::text,'')`).Scan(&oldTable, &newTable); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select views.sql,pg_get_viewdef('app.rename_rollback_view'::regclass,true)
		from pgbase_meta.collection_views views join pgbase_meta.collections collections on collections.id=views.collection_id
		where collections.name='rename_rollback_view'`).Scan(&storedSQL, &physicalSQL); err != nil {
		t.Fatal(err)
	}
	if oldTable != "app.rename_rollback_source" || newTable != "" || storedSQL != `select id,title from app.rename_rollback_source` ||
		!strings.Contains(physicalSQL, "rename_rollback_source") || strings.Contains(physicalSQL, "renamed_rollback_source") {
		t.Fatalf("old=%q new=%q stored=%q physical=%q", oldTable, newTable, storedSQL, physicalSQL)
	}
}
