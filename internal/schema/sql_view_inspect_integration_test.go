//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestInspectSQLViewInfersFieldsDependenciesAndLeavesTransactionUsable(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	source := applyFreshPlan(t, service, CreateCollectionInput{Name: "sql_view_items", Fields: []FieldInput{{Name: "amount", Type: TypeBigInt}, {Name: "category", Type: TypeText}}}, administratorID)
	if _, err := pool.Exec(context.Background(), `insert into app.sql_view_items(id,created_at,updated_at,amount,category) values(gen_random_uuid(),now(),now(),2,'a')`); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		inspection, err := InspectSQLView(context.Background(), tx, catalog, `select (array_agg(id order by id))[1] as id, min(created_at) as created_at, max(updated_at) as updated_at, category, sum(amount)::bigint as total from app.sql_view_items group by category`)
		if err != nil {
			return err
		}
		if len(inspection.Dependencies) != 1 || inspection.Dependencies[0] != source.ID {
			t.Fatalf("dependencies=%v", inspection.Dependencies)
		}
		if len(inspection.Fields) != 2 || inspection.Fields[0].Name != "category" || inspection.Fields[0].Type != TypeText || inspection.Fields[1].Type != TypeBigInt {
			t.Fatalf("fields=%#v", inspection.Fields)
		}
		var one int
		return tx.QueryRow(context.Background(), "select 1").Scan(&one)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInspectSQLViewRejectsDuplicateIdentityAndVolatileFunctions(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "sql_view_unsafe", Fields: []FieldInput{{Name: "label", Type: TypeText}}}, administratorID)
	if _, err := pool.Exec(context.Background(), `insert into app.sql_view_unsafe(id,created_at,updated_at,label) values(gen_random_uuid(),now(),now(),'a'),(gen_random_uuid(),now(),now(),'b')`); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`select id,created_at,updated_at,label from app.sql_view_unsafe union all select id,created_at,updated_at,label from app.sql_view_unsafe`,
		`select gen_random_uuid() as id,created_at,updated_at,label from app.sql_view_unsafe`,
		`select id,created_at,updated_at,label from app.sql_view_unsafe where "pg_sleep"(0) is null`,
		`select id,created_at,updated_at,label from app.sql_view_unsafe where pg_sleep/*comment*/(0) is null`,
	} {
		err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := InspectSQLView(context.Background(), tx, catalog, sql)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("InspectSQLView(%q)=%v", sql, err)
			}
			var id uuid.UUID
			return tx.QueryRow(context.Background(), "select gen_random_uuid()").Scan(&id)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectSQLViewRejectsOnlyReferencedPasswordColumns(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	minimum := 8
	applyFreshPlan(t, service, CreateCollectionInput{Name: "sql_view_passwords", Fields: []FieldInput{{Name: "label", Type: TypeText}, {Name: "secret", Type: TypePassword, Options: &FieldOptions{MinLength: &minimum}}}}, administratorID)
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := InspectSQLView(context.Background(), tx, catalog, `select id,created_at,updated_at,label from app.sql_view_passwords`); err != nil {
			return err
		}
		if _, err := InspectSQLView(context.Background(), tx, catalog, `select id,created_at,updated_at,secret from app.sql_view_passwords`); !errors.Is(err, ErrConflict) {
			t.Fatalf("password SQL view error=%v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInspectSQLViewRejectsPinnedCatalogsAndWholeRowPasswords(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "inspect_secrets", Fields: []FieldInput{{Name: "label", Type: TypeText}, {Name: "secret", Type: TypePassword}}}, actor)
	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`select '00000000-0000-0000-0000-000000000001'::uuid id, relname::text label from pg_catalog.pg_class limit 1`,
		`select id, row_to_json(s)::text label from app.inspect_secrets s`,
		`select id, to_jsonb(s) label from app.inspect_secrets s`,
		`with hidden as (select * from pg_catalog.pg_class) select '00000000-0000-0000-0000-000000000001'::uuid id, relname::text label from hidden limit 1`,
		`select id,label from app.inspect_secrets where exists(select 1 from pg_catalog.pg_class)`,
	} {
		if err := pool.InTx(t.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := InspectSQLView(t.Context(), tx, catalog, statement); !errors.Is(err, ErrConflict) {
				t.Fatalf("unsafe query accepted: %s: %v", statement, err)
			}
			var one int
			return tx.QueryRow(t.Context(), "select 1").Scan(&one)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`select id,label from app.inspect_secrets`,
		`select '00000000-0000-0000-0000-000000000001'::uuid id,count(*)::bigint total from app.inspect_secrets`,
	} {
		if err := pool.InTx(t.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error { _, err := InspectSQLView(t.Context(), tx, catalog, statement); return err }); err != nil {
			t.Fatalf("safe query rejected: %s: %v", statement, err)
		}
	}
}

func TestPersistedSQLViewsAreRecheckedOnUpgradeAndThroughWrappers(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "view_sources", Fields: []FieldInput{{Name: "label", Type: TypeText}}}, actor)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "saved_view", Type: CollectionTypeView, View: &ViewInput{SQL: `select id,label from app.view_sources`}}, actor)
	if _, err := pool.Exec(t.Context(), `create or replace view app.saved_view as select '00000000-0000-0000-0000-000000000001'::uuid id,'1970-01-01'::timestamptz created_at,'1970-01-01'::timestamptz updated_at,relname::text collate "default" label from pg_catalog.pg_class limit 1`); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(t.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := InspectSQLView(t.Context(), tx, catalog, `select id,label from app.saved_view`)
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsafe saved wrapper=%v", err)
	}
	if err := PreflightCatalog(t.Context(), pool, &Cache{}); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("unsafe saved view startup=%v", err)
	}
}
