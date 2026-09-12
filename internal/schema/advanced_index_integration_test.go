//go:build integration

package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestAdvancedRawIndexAppliesAndRoundTripsWithoutLosingSemantics(t *testing.T) {
	ctx := context.Background()
	sourcePool, source, sourceAdmin := newSchemaServiceTest(t)
	input := CreateCollectionInput{
		Name: "raw_index_roundtrip",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true},
			{Name: "score", Type: TypeBigInt, Required: true},
		},
		Indexes: []IndexInput{{
			Name:   "semantic_index",
			Unique: true,
			RawSQL: `CREATE UNIQUE INDEX semantic_index ON raw_index_roundtrip ((lower(title)) COLLATE "C" DESC, score) WHERE title IS NOT NULL`,
		}},
	}
	created := applyFreshPlan(t, source, input, sourceAdmin)
	if len(created.Indexes) != 1 {
		t.Fatalf("created indexes = %+v", created.Indexes)
	}
	sourceDefinition := postgresIndexDefinition(t, sourcePool, created.TableName, created.Indexes[0].IndexName)
	assertRawIndexDefinition(t, sourceDefinition)

	if _, err := sourcePool.Exec(ctx, `insert into app.raw_index_roundtrip(id,created_at,updated_at,title,score) values
		(gen_random_uuid(),now(),now(),'MixedCase',7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sourcePool.Exec(ctx, `insert into app.raw_index_roundtrip(id,created_at,updated_at,title,score) values
		(gen_random_uuid(),now(),now(),'mixedcase',7)`); err == nil {
		t.Fatal("case-folded duplicate was accepted by the unique expression index")
	} else if postgresErr, ok := err.(*pgconn.PgError); !ok || postgresErr.Code != "23505" {
		t.Fatalf("duplicate error = %v", err)
	}

	document, err := source.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Collections) != 1 || len(document.Collections[0].Indexes) != 1 {
		t.Fatalf("exported document = %+v", document)
	}
	exported := document.Collections[0].Indexes[0]
	if exported.Name != "semantic_index" || !exported.Unique {
		t.Fatalf("exported index = %+v", exported)
	}
	assertRawIndexDefinition(t, exported.RawSQL)

	targetPool, target, targetAdmin := newSchemaServiceTest(t)
	plan, err := target.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplySchemaImport(ctx, SchemaImportIntent{
		Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum,
		Actor: targetAdmin, OperationID: uuid.New(), IdempotencyKey: "raw-index-roundtrip-0001",
	}); err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := target.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetCollection := targetCatalog.Collections[input.Name]
	if len(targetCollection.Indexes) != 1 {
		t.Fatalf("target indexes = %+v", targetCollection.Indexes)
	}
	targetDefinition := postgresIndexDefinition(t, targetPool, targetCollection.TableName, targetCollection.Indexes[0].IndexName)
	assertRawIndexDefinition(t, targetDefinition)
	if sourceDefinition != targetDefinition {
		t.Fatalf("physical index changed after export/import:\nsource: %s\ntarget: %s", sourceDefinition, targetDefinition)
	}
	if err := PreflightCatalog(ctx, targetPool, &Cache{}); err != nil {
		t.Fatalf("target catalog preflight: %v", err)
	}
}

func postgresIndexDefinition(t *testing.T, db *database.Pool, table, index string) string {
	t.Helper()
	var definition string
	if err := db.QueryRow(context.Background(), `select pg_get_indexdef(indexes.indexrelid)
		from pg_index as indexes
		join pg_class as tables on tables.oid=indexes.indrelid
		join pg_namespace as namespaces on namespaces.oid=tables.relnamespace
		join pg_class as index_tables on index_tables.oid=indexes.indexrelid
		where namespaces.nspname='app' and tables.relname=$1 and index_tables.relname=$2`, table, index).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	return definition
}

func assertRawIndexDefinition(t *testing.T, definition string) {
	t.Helper()
	normalized := strings.ToLower(definition)
	for _, fragment := range []string{"unique index", "lower(title)", `collate "c"`, "desc", "score", "where", "title is not null"} {
		if !strings.Contains(normalized, fragment) {
			t.Errorf("index definition %q does not contain %q", definition, fragment)
		}
	}
}
