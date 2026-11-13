//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func TestSingleChangeKeepsAuthCollectionAndRealmTogether(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	importAuth := func(name string) uuid.UUID {
		value := service.Scaffolds().Auth
		value.Collection.Name, value.AuthCollection.Name = name, name
		document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{value.Collection}, AuthCollections: []SchemaDocumentAuthCollection{value.AuthCollection}}
		plan, err := service.PreviewSchemaImport(ctx, document)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "pair-" + name + "-00000001"}); err != nil {
			t.Fatal(err)
		}
		var realm uuid.UUID
		if err = pool.QueryRow(ctx, `select id from pgbase_meta.auth_collections where name=$1`, name).Scan(&realm); err != nil {
			t.Fatal(err)
		}
		return realm
	}
	apply := func(change Change) error {
		plan, err := service.Preview(ctx, change)
		if err != nil {
			return err
		}
		_, err = service.Apply(ctx, LegacyApply(change, plan, admin))
		return err
	}
	detail := func(err error) string {
		var invalid *ValidationError
		if !errors.As(err, &invalid) || len(invalid.Details) != 1 {
			return ""
		}
		return invalid.Details[0].Field + ":" + invalid.Details[0].Code
	}
	realmNames := func() map[string]bool {
		rows, err := pool.Query(ctx, `select name from pgbase_meta.auth_collections`)
		if err != nil {
			t.Fatal(err)
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, name := range names {
			out[name] = true
		}
		return out
	}

	create := NewCreateChange(CreateCollectionInput{Name: "plan_auth", Type: CollectionTypeAuth})
	if _, err := service.Preview(ctx, create); detail(err) != "type:auth_requires_schema_import" {
		t.Fatalf("plan auth create = %v", err)
	}
	if _, err := service.Apply(ctx, LegacyApply(create, Plan{}, admin)); detail(err) != "type:auth_requires_schema_import" {
		t.Fatalf("apply auth create = %v", err)
	}

	realm := importAuth("pair_auth")
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json='[{"name":"friend","type":"relation","relation":{"targetCollection":"pair_auth","cardinality":"single","onDelete":"restrict"}}]' where name='users'`); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := catalog.Collections["pair_auth"].ID
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,created_at,updated_at) values($1,'pair_taken',now(),now())`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err = apply(NewUpdateChange(UpdateCollectionInput{Name: "pair_taken", CollectionID: &id})); detail(err) != "name:auth_collection_name_taken" {
		t.Fatalf("rename onto another realm = %v", err)
	}
	if err = apply(NewUpdateChange(UpdateCollectionInput{Name: "pair_renamed", CollectionID: &id})); err != nil {
		t.Fatal(err)
	}
	var renamedID uuid.UUID
	var target string
	if err = pool.QueryRow(ctx, `select id,(select fields_json->0->'relation'->>'targetCollection' from pgbase_meta.auth_collections where name='users') from pgbase_meta.auth_collections where name='pair_renamed'`).Scan(&renamedID, &target); err != nil || renamedID != realm || target != "pair_renamed" || realmNames()["pair_auth"] {
		t.Fatalf("renamed realm id=%s target=%q err=%v", renamedID, target, err)
	}

	if _, err = pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,'pair@example.test','fixture',true,now(),now())`, uuid.New(), realm); err != nil {
		t.Fatal(err)
	}
	if err = apply(NewDeleteChange("pair_renamed")); detail(err) != "name:auth_collection_has_users" {
		t.Fatalf("delete with users = %v", err)
	}
	if _, err = pool.Exec(ctx, `delete from pgbase_meta.users where auth_collection_id=$1`, realm); err != nil {
		t.Fatal(err)
	}
	if err = apply(NewDeleteChange("pair_renamed")); err != nil {
		t.Fatal(err)
	}
	if realmNames()["pair_renamed"] {
		t.Fatal("deleting the auth collection left its realm behind")
	}
}
