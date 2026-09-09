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
