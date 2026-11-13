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
	// Another realm's relation field still targets it (BA2): refused by name, at plan and apply.
	if err = apply(NewDeleteChange("pair_renamed")); detail(err) != "authCollections.users.fields.friend:collection_referenced" {
		t.Fatalf("delete while a realm relation targets it = %v", err)
	}
	setFriend := func(fields string) {
		if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=$1::jsonb where name='users'`, fields); err != nil {
			t.Fatal(err)
		}
	}
	setFriend(`[]`)
	deletePlan, err := service.Preview(ctx, NewDeleteChange("pair_renamed"))
	if err != nil {
		t.Fatal(err)
	}
	setFriend(`[{"name":"friend","type":"relation","relation":{"targetCollection":"pair_renamed","cardinality":"single","onDelete":"restrict"}}]`)
	if _, err = service.Apply(ctx, LegacyApply(NewDeleteChange("pair_renamed"), deletePlan, admin)); detail(err) != "authCollections.users.fields.friend:collection_referenced" {
		t.Fatalf("apply delete while a realm relation targets it = %v", err)
	}
	setFriend(`[]`)
	if err = apply(NewDeleteChange("pair_renamed")); err != nil {
		t.Fatal(err)
	}
	if realmNames()["pair_renamed"] {
		t.Fatal("deleting the auth collection left its realm behind")
	}

	// An auth collection without a realm (created before pairing) may not adopt an unrelated realm by rename (BA5).
	importAuth("pair_orphan")
	if _, err = pool.Exec(ctx, `delete from pgbase_meta.auth_collections where name='pair_orphan'`); err != nil {
		t.Fatal(err)
	}
	if catalog, err = service.Catalog(ctx); err != nil {
		t.Fatal(err)
	}
	orphan := catalog.Collections["pair_orphan"].ID
	if err = apply(NewUpdateChange(UpdateCollectionInput{Name: "pair_taken", CollectionID: &orphan})); detail(err) != "name:auth_collection_name_taken" {
		t.Fatalf("realm-less rename onto an unrelated realm = %v", err)
	}
	if err = apply(NewUpdateChange(UpdateCollectionInput{Name: "pair_free", CollectionID: &orphan})); err != nil || realmNames()["pair_free"] {
		t.Fatalf("realm-less rename onto a free name = %v", err)
	}
}

func TestDesignerFieldRenameNeverOverwritesRealmOnlyField(t *testing.T) {
	pool, service, admin := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	value := service.Scaffolds().Auth
	value.Collection.Name, value.AuthCollection.Name = "nick_auth", "nick_auth"
	value.Collection.Fields = append(value.Collection.Fields, FieldInput{Name: "nickname", Type: TypeText})
	value.AuthCollection.Fields = []FieldInput{{Name: "nickname", Type: TypeText}}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{value.Collection}, AuthCollections: []SchemaDocumentAuthCollection{value.AuthCollection}}
	imported, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: imported.BaseVersion, Checksum: imported.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "nick-auth-import-0001"}); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []FieldInput{}
	for _, field := range catalog.Collections["nick_auth"].Fields {
		id, system := field.ID, field.System
		input := FieldInput{ID: &id, Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Hidden: field.Hidden, System: &system}
		if field.Name == "nickname" {
			input.Name = "nick"
		}
		inputs = append(inputs, input)
	}
	rename := NewUpdateChange(UpdateCollectionInput{Name: "nick_auth", Fields: inputs})
	plan, err := service.Preview(ctx, rename)
	if err != nil {
		t.Fatal(err)
	}
	// A realm-only field with users' values appears after the plan (BA3).
	user := uuid.New()
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=fields_json||'[{"name":"nick","type":"text"}]' where name='nick_auth'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) select $1,id,'nick@example.test','fixture',true,now(),now() from pgbase_meta.auth_collections where name='nick_auth'`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,'{"nickname":"moved","nick":"kept"}',now())`, user); err != nil {
		t.Fatal(err)
	}
	want := "fields.nick:realm_field_exists"
	if _, err = service.Apply(ctx, LegacyApply(rename, plan, admin)); !hasDetail(err, want) {
		t.Fatalf("apply rename onto a realm-only field = %v", err)
	}
	if _, err = service.Preview(ctx, rename); !hasDetail(err, want) {
		t.Fatalf("plan rename onto a realm-only field = %v", err)
	}
	var attributes string
	if err = pool.QueryRow(ctx, `select attributes::text from pgbase_meta.user_auth_profiles where user_id=$1`, user).Scan(&attributes); err != nil || attributes != `{"nick": "kept", "nickname": "moved"}` {
		t.Fatalf("profile after refused rename = %s, %v", attributes, err)
	}
}

func TestSchemaImportRefusesRealmRelationWithoutTarget(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	value := service.Scaffolds().Auth
	value.Collection.Name, value.AuthCollection.Name = "dangle_auth", "dangle_auth"
	value.AuthCollection.Fields = []FieldInput{{Name: "ghost", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "no_such_collection", Cardinality: RelationSingle}}}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{value.Collection}, AuthCollections: []SchemaDocumentAuthCollection{value.AuthCollection}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "dangle-import-0001"}); !hasDetail(err, "authCollections.dangle_auth.fields.ghost:invalid") {
		t.Fatalf("import of a realm relation without a target = %v", err)
	}
}

func hasDetail(err error, want string) bool {
	var invalid *ValidationError
	return errors.As(err, &invalid) && len(invalid.Details) == 1 && invalid.Details[0].Field+":"+invalid.Details[0].Code == want
}
