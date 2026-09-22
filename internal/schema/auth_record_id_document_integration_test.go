//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestSchemaDocumentImportedAuthTextRecordIDMatchesCatalogAndRoundTrips(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	policy := RecordIDPolicy{Kind: RecordIDText, MinLength: 4, MaxLength: 12, Pattern: `^[A-Za-z0-9_]+$`, AutogeneratePattern: `[A-Z]{8}`}
	auth := schemaDocumentAuthCollectionForTest()
	auth.Name = "members"
	document := SchemaDocument{
		Format:          SchemaDocumentFormat,
		Collections:     []SchemaDocumentCollection{{Name: auth.Name, Type: CollectionTypeAuth, RecordID: &policy, Rules: json.RawMessage(`{}`)}},
		AuthCollections: []SchemaDocumentAuthCollection{auth},
	}
	applyAuthRecordIDDocument(t, service, actor, document)
	assertDocumentAuthRecordID(t, pool, service, auth.Name, policy)
	document.Collections[0].RecordID = nil
	document.AuthCollections[0].RegistrationEnabled = true
	applyAuthRecordIDDocument(t, service, actor, document)
	assertDocumentAuthRecordID(t, pool, service, auth.Name, policy)

	exported, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	targetPool, target, targetActor := newSchemaServiceTest(t)
	applyAuthRecordIDDocument(t, target, targetActor, exported)
	assertDocumentAuthRecordID(t, targetPool, target, auth.Name, policy)
}

func TestSchemaDocumentStandaloneAuthTextRecordIDRoundTrips(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	policy := DefaultTextRecordIDPolicy()
	auth := schemaDocumentAuthCollectionForTest()
	auth.Name = "standalone_members"
	wire, err := json.Marshal(map[string]any{
		"format": SchemaDocumentFormat,
		"authCollections": []any{struct {
			SchemaDocumentAuthCollection
			RecordID RecordIDPolicy `json:"recordId"`
		}{auth, policy}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var document SchemaDocument
	if err := json.Unmarshal(wire, &document); err != nil {
		t.Fatal(err)
	}
	applyAuthRecordIDDocument(t, service, actor, document)
	assertAuthAuthorityRecordID(t, pool, auth.Name, policy)
	exported, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Collections) != 0 {
		t.Fatalf("standalone auth import created physical collections: %+v", exported.Collections)
	}
	targetPool, target, targetActor := newSchemaServiceTest(t)
	applyAuthRecordIDDocument(t, target, targetActor, exported)
	assertAuthAuthorityRecordID(t, targetPool, auth.Name, policy)
}

func TestSchemaDocumentAuthRecordIDRejectsConflictsAndPreservesOmission(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	policy := DefaultTextRecordIDPolicy()
	auth := schemaDocumentAuthCollectionForTest()
	auth.Name, auth.RecordID = "members", &policy
	document := SchemaDocument{Format: SchemaDocumentFormat, AuthCollections: []SchemaDocumentAuthCollection{auth}}
	applyAuthRecordIDDocument(t, service, actor, document)

	conflict := cloneSchemaDocument(document)
	uuidPolicy := DefaultRecordIDPolicy()
	conflict.AuthCollections[0].RecordID = &uuidPolicy
	if _, err := service.PreviewSchemaImport(context.Background(), conflict); err == nil {
		t.Fatal("preview accepted an immutable auth ID policy change")
	}
	if _, err := service.ApplySchemaImport(context.Background(), SchemaImportIntent{
		Document: conflict, Checksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Actor: actor, OperationID: uuid.New(), IdempotencyKey: uuid.NewString(),
	}); err == nil {
		t.Fatal("apply accepted an immutable auth ID policy change")
	}
	assertAuthAuthorityRecordID(t, pool, auth.Name, policy)

	conflict = cloneSchemaDocument(document)
	conflict.Collections = []SchemaDocumentCollection{{Name: auth.Name, Type: CollectionTypeAuth, Rules: json.RawMessage(`{}`)}}
	if _, err := service.PreviewSchemaImport(context.Background(), conflict); err == nil {
		t.Fatal("preview accepted disagreeing auth and catalog ID policies")
	}

	document.AuthCollections[0].RecordID = nil
	document.AuthCollections[0].RegistrationEnabled = true
	applyAuthRecordIDDocument(t, service, actor, document)
	assertAuthAuthorityRecordID(t, pool, auth.Name, policy)

	// A base collection is independent of a same-named standalone auth collection.
	document.Collections = []SchemaDocumentCollection{{Name: auth.Name, Type: CollectionTypeBase, Rules: json.RawMessage(`{}`)}}
	applyAuthRecordIDDocument(t, service, actor, document)
	assertAuthAuthorityRecordID(t, pool, auth.Name, policy)
}

func TestSchemaDocumentAuthRecordIDApplyRejectsAuthorityCreatedAfterPreview(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	policy := DefaultTextRecordIDPolicy()
	auth := schemaDocumentAuthCollectionForTest()
	auth.Name = "members"
	document := SchemaDocument{
		Format:          SchemaDocumentFormat,
		Collections:     []SchemaDocumentCollection{{Name: auth.Name, Type: CollectionTypeAuth, RecordID: &policy, Rules: json.RawMessage(`{}`)}},
		AuthCollections: []SchemaDocumentAuthCollection{auth},
	}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,created_at,updated_at) values($1,$2,current_timestamp,current_timestamp)`, uuid.New(), auth.Name); err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{
		Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum,
		Actor: actor, OperationID: uuid.New(), IdempotencyKey: uuid.NewString(),
	})
	assertAuthValidationPath(t, err, "authCollections.members.recordId")
	assertAuthAuthorityRecordID(t, pool, auth.Name, DefaultRecordIDPolicy())
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := catalog.Collections[auth.Name]; exists || catalog.Version != plan.BaseVersion {
		t.Fatalf("rejected import changed catalog: %+v", catalog)
	}
	var physicalExists bool
	if err := pool.QueryRow(ctx, `select to_regclass('app.members') is not null`).Scan(&physicalExists); err != nil {
		t.Fatal(err)
	}
	if physicalExists {
		t.Fatal("rejected import left a physical table")
	}
}

func applyAuthRecordIDDocument(t *testing.T, service *Service, actor uuid.UUID, document SchemaDocument) {
	t.Helper()
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatalf("preview auth record ID document: %#v", err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{
		Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum,
		Actor: actor, OperationID: uuid.New(), IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("apply auth record ID document: %v", err)
	}
}

func assertDocumentAuthRecordID(t *testing.T, pool *database.Pool, service *Service, name string, want RecordIDPolicy) {
	t.Helper()
	ctx := context.Background()
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Collections[name].RecordID; got != want {
		t.Fatalf("catalog record ID policy = %+v, want %+v", got, want)
	}
	assertAuthAuthorityRecordID(t, pool, name, want)
}

func assertAuthAuthorityRecordID(t *testing.T, pool *database.Pool, name string, want RecordIDPolicy) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `select record_id from pgbase_meta.auth_collections where name=$1`, name).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got RecordIDPolicy
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("auth authority record ID policy = %+v, want %+v", got, want)
	}
}
