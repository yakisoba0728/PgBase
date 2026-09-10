//go:build integration

package schema

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestCompoundIndexPlanCatalogManifestAndPostgresUse(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	input := CreateCollectionInput{Name: "compound_records", Fields: []FieldInput{{Name: "tenant", Type: TypeUUID, Required: true}, {Name: "sequence", Type: TypeBigInt, Required: true}, {Name: "payload", Type: TypeText}}, Indexes: []IndexInput{{Name: "tenant_sequence", Fields: []string{"tenant", "sequence"}, Unique: true}}}
	plan, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(plan.Statements, `CREATE UNIQUE INDEX "`+compoundIndexName(input.Name, "tenant_sequence")+`" ON "app"."compound_records" ("tenant", "sequence")`) {
		t.Fatalf("plan SQL=%v", plan.Statements)
	}
	for _, statement := range plan.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	collection, err := newCollectionMetadata(plan.Definition)
	if err != nil {
		t.Fatal(err)
	}
	collection.CreatedAt = time.Now().UTC()
	if err := NewStore(pool).InsertCollection(ctx, collection, plan.Definition, collection.CreatedAt); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewStore(pool).ReadCatalog(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Collections[input.Name]
	if len(got.Indexes) != 1 || strings.Join(got.Indexes[0].Fields, ",") != "tenant,sequence" || !got.Indexes[0].Unique {
		t.Fatalf("catalog indexes=%+v", got.Indexes)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, index := range manifest.Required().Indexes {
		if index.Name == compoundIndexName(input.Name, "tenant_sequence") {
			found = len(index.Keys) == 2 && index.Keys[0].Column == "tenant" && index.Keys[1].Column == "sequence"
			assertPostgresIndexSpec(t, pool, index)
		}
	}
	if !found {
		t.Fatal("compound index absent from physical manifest")
	}
	if _, err := pool.Exec(ctx, `insert into app.compound_records(id,created_at,updated_at,tenant,sequence,payload) select gen_random_uuid(),now(),now(),gen_random_uuid(),g,'x' from generate_series(1,2000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `analyze app.compound_records`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `set enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	var explain string
	rows, err := pool.Query(ctx, `explain (format text) select * from app.compound_records where tenant=(select tenant from app.compound_records limit 1) and sequence=1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		explain += line
	}
	rows.Close()
	if !strings.Contains(explain, compoundIndexName(input.Name, "tenant_sequence")) {
		t.Fatalf("EXPLAIN did not use compound index: %s", explain)
	}
	empty := []IndexInput{}
	update, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: input.Name, Fields: input.Fields, Indexes: &empty}))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQLPrefix(update.Statements, `DROP INDEX IF EXISTS "app"."`+compoundIndexName(input.Name, "tenant_sequence")+`"`) {
		t.Fatalf("drop plan=%v", update.Statements)
	}
	for _, statement := range update.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	updatedFields, err := updatedFieldMetadata(collection.ID, collection.Fields, update.Definition)
	if err != nil {
		t.Fatal(err)
	}
	collection.Fields = updatedFields
	collection.Indexes = nil
	if err := NewStore(pool).UpdateCollection(ctx, collection, update.Definition, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	updatedCatalog, err := NewStore(pool).ReadCatalog(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedCatalog.Collections[input.Name].Indexes) != 0 {
		t.Fatalf("removed compound index persisted: %+v", updatedCatalog.Collections[input.Name].Indexes)
	}
	preserved, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: input.Name, Fields: input.Fields}))
	if err != nil {
		t.Fatal(err)
	}
	if len(preserved.Definition.Indexes) != 1 {
		t.Fatalf("omitted indexes did not preserve: %+v", preserved.Definition.Indexes)
	}
}

func TestCompoundIndexServiceApplyPersistsMatchingIdentity(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t)
	fields := []FieldInput{{Name: "tenant", Type: TypeUUID}, {Name: "sequence", Type: TypeBigInt}}
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "compound_apply", Fields: fields, Indexes: []IndexInput{{Name: "lookup", Fields: []string{"tenant", "sequence"}}}}, administratorID)
	if len(created.Indexes) != 1 {
		t.Fatalf("created indexes=%+v", created.Indexes)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stored := catalog.Collections["compound_apply"].Indexes
	if len(stored) != 1 || stored[0].ID != created.Indexes[0].ID {
		t.Fatalf("created/stored index identity=%+v/%+v", created.Indexes, stored)
	}
	empty := []IndexInput{}
	input := UpdateCollectionInput{Name: "compound_apply", Fields: fields, Indexes: &empty}
	plan, err := service.Preview(context.Background(), NewUpdateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := applyUpdate(context.Background(), service, input, plan, administratorID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Indexes) != 0 {
		t.Fatalf("updated indexes=%+v", updated.Indexes)
	}
}
func containsSQL(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
func containsSQLPrefix(items []string, want string) bool {
	for _, item := range items {
		if strings.HasPrefix(item, want) {
			return true
		}
	}
	return false
}
