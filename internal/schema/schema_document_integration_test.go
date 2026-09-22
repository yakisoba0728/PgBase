//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaDocumentImportUpdatesDeclarativeView(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	policy := RecordIDPolicy{Kind: RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`, AutogeneratePattern: `[A-Z]{8}`}
	source := applyFreshPlan(t, service, CreateCollectionInput{Name: "import_view_source", RecordID: &policy, Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "score", Type: TypeInteger}}}, actor)
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "import_builder_view", View: &ViewInput{Source: source.Name, Projection: []string{"title"}}}, actor)
	rowID := uuid.New()
	if _, err := pool.Exec(ctx, `insert into app.import_view_source(id,created_at,updated_at,title,score) values($1,now(),now(),'visible',7),(gen_random_uuid(),now(),now(),'hidden',2)`, rowID); err != nil {
		t.Fatal(err)
	}
	titleID := view.Fields[0].ID
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{
		Name:   view.Name,
		View:   &ViewInput{Source: source.Name, Projection: []string{"title", "score"}, Filter: &ViewFilter{Op: "gte", Field: "score", Value: json.RawMessage(`5`)}},
		Fields: []FieldInput{{Name: "title", Type: TypeText, Hidden: true, Help: "Imported title"}, {Name: "score", Type: TypeInteger, Presentable: true}},
		Rules:  ownerRuleDocument(),
	}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Operation != updateCollectionOperation || plan.Changes[0].Definition.RecordID != policy {
		t.Fatalf("preview changes=%#v", plan.Changes)
	}
	if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-declarative-view-update-0001", ValidateRules: func(Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated := catalog.Collections[view.Name]
	if updated.ID != view.ID || updated.RecordID != policy || len(updated.Fields) != 2 || updated.Fields[0].ID != titleID || !updated.Fields[0].Hidden || updated.Fields[0].Help != "Imported title" || !updated.Fields[1].Presentable {
		t.Fatalf("updated view=%#v", updated)
	}
	var id uuid.UUID
	var title string
	var score int
	if err := pool.QueryRow(ctx, `select id,title,score from app.import_builder_view`).Scan(&id, &title, &score); err != nil || id != rowID || title != "visible" || score != 7 {
		t.Fatalf("row=%q/%q/%d err=%v", id, title, score, err)
	}
}

func TestSchemaDocumentImportUpdatesDeclarativeViewPresentationWithDependentView(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	applyFreshPlan(t, service, CreateCollectionInput{Name: "presentation_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, actor)
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "presentation_builder_view", View: &ViewInput{Source: "presentation_source", Projection: []string{"title"}}}, actor)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "presentation_dependent_view", View: &ViewInput{SQL: `select id,title from app.presentation_builder_view`}}, actor)
	if _, err := pool.Exec(ctx, `insert into app.presentation_source(id,created_at,updated_at,title) values(gen_random_uuid(),now(),now(),'preserved')`); err != nil {
		t.Fatal(err)
	}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{
		Name: view.Name, View: &ViewInput{Source: "presentation_source", Projection: []string{"title"}},
		Fields: []FieldInput{{Name: "title", Type: TypeText, Hidden: true, Help: "Imported presentation"}}, Rules: ownerRuleDocument(),
	}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || len(plan.Changes[0].Statements) != 0 {
		t.Fatalf("presentation preview=%#v", plan.Changes)
	}
	if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-view-presentation-dependent-0001", ValidateRules: func(Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated := catalog.Collections[view.Name]
	if updated.ID != view.ID || updated.Fields[0].ID != view.Fields[0].ID || !updated.Fields[0].Hidden || updated.Fields[0].Presentable || updated.Fields[0].Help != "Imported presentation" {
		t.Fatalf("updated presentation=%#v", updated)
	}
	var title string
	if err := pool.QueryRow(ctx, `select title from app.presentation_dependent_view`).Scan(&title); err != nil || title != "preserved" {
		t.Fatalf("dependent row=%q err=%v", title, err)
	}
}

func TestSchemaDocumentDeclarativeViewUpdateRejectsInvalidDependentAndStalePlans(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	applyFreshPlan(t, service, CreateCollectionInput{Name: "guarded_view_source", Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "score", Type: TypeInteger}}}, actor)
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "guarded_builder_view", View: &ViewInput{Source: "guarded_view_source", Projection: []string{"title"}}}, actor)
	if _, err := pool.Exec(ctx, `insert into app.guarded_view_source(id,created_at,updated_at,title,score) values(gen_random_uuid(),now(),now(),'kept',9)`); err != nil {
		t.Fatal(err)
	}

	invalid := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: view.Name, View: &ViewInput{Source: "guarded_view_source", Projection: []string{"missing"}}, Rules: ownerRuleDocument()}}}
	if _, err := service.PreviewSchemaImport(ctx, invalid); err == nil {
		t.Fatal("invalid projection was accepted")
	}

	applyFreshPlan(t, service, CreateCollectionInput{Name: "guarded_dependent_view", View: &ViewInput{SQL: `select id,title from app.guarded_builder_view`}}, actor)
	blocked := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: view.Name, View: &ViewInput{Source: "guarded_view_source", Projection: []string{"title", "score"}}, Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "score", Type: TypeInteger}}, Rules: ownerRuleDocument()}}}
	if _, err := service.PreviewSchemaImport(ctx, blocked); err == nil {
		t.Fatal("update with a dependent view was accepted")
	}
	applyChange(t, service, NewDeleteChange("guarded_dependent_view"), actor)
	plan, err := service.PreviewSchemaImport(ctx, blocked)
	if err != nil {
		t.Fatal(err)
	}
	applyFreshPlan(t, service, CreateCollectionInput{Name: "advance_after_view_preview"}, actor)
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: blocked, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-declarative-view-stale-0001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale error=%v", err)
	}
	var title string
	if err := pool.QueryRow(ctx, `select title from app.guarded_builder_view`).Scan(&title); err != nil || title != "kept" {
		t.Fatalf("preserved view row=%q err=%v", title, err)
	}
}

func TestSchemaDocumentDeclarativeViewUpdateRollsBackAfterLateValidationFailure(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	applyFreshPlan(t, service, CreateCollectionInput{Name: "rollback_view_source", Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "score", Type: TypeInteger}}}, actor)
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "rollback_builder_view", View: &ViewInput{Source: "rollback_view_source", Projection: []string{"title"}}}, actor)
	if _, err := pool.Exec(ctx, `insert into app.rollback_view_source(id,created_at,updated_at,title,score) values(gen_random_uuid(),now(),now(),'original',4)`); err != nil {
		t.Fatal(err)
	}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: view.Name, View: &ViewInput{Source: "rollback_view_source", Projection: []string{"score"}}, Fields: []FieldInput{{Name: "score", Type: TypeInteger, Hidden: true}}, Rules: ownerRuleDocument()}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-declarative-view-rollback-0001", ValidateRules: func(Collection, json.RawMessage) error { return errors.New("reject") }})
	if err == nil {
		t.Fatal("late validation failure was accepted")
	}
	service = NewService(pool, &Cache{}, nil)
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Collections[view.Name]
	if got.ID != view.ID || len(got.Fields) != 1 || got.Fields[0].Name != "title" || got.Fields[0].Hidden {
		t.Fatalf("rolled back catalog=%#v", got)
	}
	var title string
	if err := pool.QueryRow(ctx, `select title from app.rollback_builder_view`).Scan(&title); err != nil || title != "original" {
		t.Fatalf("rolled back row=%q err=%v", title, err)
	}
}

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
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
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
	target.SetAuthExecutablePolicyValidator(authExecutableValidator{})
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

func TestSchemaDocumentImportAppliesCollectionMetadataOnlyChange(t *testing.T) {
	_, service, actor := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	applyFreshPlan(t, service, CreateCollectionInput{Name: "metadata_import", CompatibilityName: "MetadataImport"}, actor)
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index := range document.Collections {
		if document.Collections[index].Name == "metadata_import" {
			document.Collections[index].CompatibilityName = "LegacyMetadataImport"
			document.Collections[index].ValidationMode = ValidationModePocketBase
		}
	}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatalf("metadata-only preview: %#v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Operation != updateCollectionOperation {
		t.Fatalf("metadata-only preview changes=%#v", plan.Changes)
	}
	if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "schema-metadata-import-0001", ValidateRules: func(Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Collections["metadata_import"]
	if got.CompatibilityName != "LegacyMetadataImport" || got.ValidationMode != ValidationModePocketBase {
		t.Fatalf("collection metadata=%+v", got)
	}
}
