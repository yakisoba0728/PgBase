//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaDocumentPreviewApplyExportWithNewSQLView(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	ownerRules := json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{
		{Name: "import_posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Presentable: true, Help: "Shown in lists"}}, UserAccess: true, Rules: ownerRules},
		{Name: "a_import_post_view", View: &ViewInput{SQL: `select id,title from app.z_import_post_view`}, DependsOn: []string{"z_import_post_view"}, Rules: ownerRules},
		{Name: "z_import_post_view", View: &ViewInput{SQL: `select id,title from app.import_posts`}, DependsOn: []string{"import_posts"}, Rules: ownerRules},
	}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	var previewTable *string
	if err := pool.QueryRow(ctx, `select to_regclass('app.import_posts')::text`).Scan(&previewTable); err != nil {
		t.Fatal(err)
	}
	if previewTable != nil {
		t.Fatalf("preview left table %q", *previewTable)
	}
	result, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-import-integration-0001", ValidateRules: func(Collection, json.RawMessage) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "committed" || result.Applied < 4 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := pool.Exec(ctx, `insert into app.import_posts(id,created_at,updated_at,title) values(gen_random_uuid(),now(),now(),'portable')`); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := pool.QueryRow(ctx, `select title from app.a_import_post_view`).Scan(&title); err != nil || title != "portable" {
		t.Fatalf("view title=%q err=%v", title, err)
	}
	applyFreshPlan(t, service, CreateCollectionInput{Name: "declarative_posts", View: &ViewInput{Source: "import_posts", Projection: []string{"title"}}}, actor)
	if _, err := pool.Exec(ctx, `update pgbase_meta.fields set hidden=true,presentable=false,help='View title' where collection_id=(select id from pgbase_meta.collections where name='a_import_post_view') and name='title'`); err != nil {
		t.Fatal(err)
	}
	service = NewService(pool, &Cache{}, nil)
	exported, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var posts SchemaDocumentCollection
	for _, item := range exported.Collections {
		if item.Name == "import_posts" {
			posts = item
		}
	}
	if len(exported.Collections) != 4 || len(posts.Fields) != 1 || !posts.Fields[0].Presentable || posts.Fields[0].Help != "Shown in lists" {
		t.Fatalf("export=%+v", exported)
	}
	if _, err := service.PreviewSchemaImport(ctx, exported); err != nil {
		t.Fatalf("own declarative export does not roundtrip: %#v", err)
	}
	_, target, targetActor := newSchemaServiceTest(t)
	targetPlan, err := target.PreviewSchemaImport(ctx, exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplySchemaImport(ctx, SchemaImportIntent{Document: exported, BaseVersion: targetPlan.BaseVersion, Checksum: targetPlan.Checksum, Actor: targetActor, OperationID: uuid.New(), IdempotencyKey: "schema-import-integration-0002", ValidateRules: func(Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := target.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	view := targetCatalog.Collections["a_import_post_view"]
	if len(view.Fields) != 1 || !view.Fields[0].Hidden || view.Fields[0].Presentable || view.Fields[0].Help != "View title" {
		t.Fatalf("view presentation=%+v", view.Fields)
	}
}
