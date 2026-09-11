//go:build integration

package schema

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestAuthCollectionScaffoldImportsCatalogAndRealm(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	value := service.Scaffolds().Auth
	value.Collection.Name = "scaffold_auth"
	value.AuthCollection.Name = value.Collection.Name
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{value.Collection}, AuthCollections: []SchemaDocumentAuthCollection{value.AuthCollection}}
	plan, err := service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApplySchemaImport(context.Background(), SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "auth-scaffold-import-0001"}); err != nil {
		t.Fatal(err)
	}
	var catalogType CollectionType
	var passwordEnabled bool
	if err = pool.QueryRow(context.Background(), `select c.collection_type,p.password_enabled from pgbase_meta.collections c join pgbase_meta.auth_collections a on a.name=c.name join pgbase_meta.auth_collection_policies p on p.auth_collection_id=a.id where c.name='scaffold_auth'`).Scan(&catalogType, &passwordEnabled); err != nil {
		t.Fatal(err)
	}
	if catalogType != CollectionTypeAuth || !passwordEnabled {
		t.Fatalf("catalog type=%q passwordEnabled=%t", catalogType, passwordEnabled)
	}
}
