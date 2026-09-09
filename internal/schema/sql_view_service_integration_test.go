//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"
)

func TestSQLViewPreviewApplyReloadQueryAndSourceGuards(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	items := applyFreshPlan(t, service, CreateCollectionInput{Name: "sql_items", Fields: []FieldInput{{Name: "category", Type: TypeText}, {Name: "amount", Type: TypeBigInt}}}, administratorID)
	labels := applyFreshPlan(t, service, CreateCollectionInput{Name: "sql_labels", Fields: []FieldInput{{Name: "category", Type: TypeText}, {Name: "label", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into app.sql_items(id,created_at,updated_at,category,amount) values
		('019551d0-e9c0-7000-8000-000000000201',now(),now(),'a',2),
		('019551d0-e9c0-7000-8000-000000000202',now(),now(),'a',3),
		('019551d0-e9c0-7000-8000-000000000203',now(),now(),'b',7);
		insert into app.sql_labels(id,created_at,updated_at,category,label) values
		(gen_random_uuid(),now(),now(),'a','Alpha'),(gen_random_uuid(),now(),now(),'b','Beta')`); err != nil {
		t.Fatal(err)
	}
	sql := `select (array_agg(i.id order by i.id))[1] as id, i.category, l.label, sum(i.amount)::bigint as total
		from app.sql_items i join app.sql_labels l on l.category=i.category group by i.category,l.label`
	input := CreateCollectionInput{Name: "sql_totals", View: &ViewInput{SQL: sql}}
	change := NewCreateChange(input)
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Definition.View == nil || plan.Definition.View.SQL != sql || len(plan.Definition.Fields) != 3 || len(plan.Definition.View.Dependencies) != 2 {
		t.Fatalf("SQL view plan=%+v", plan.Definition)
	}
	result, err := service.Apply(ctx, LegacyApply(change, plan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Collection.IsSQLView() || len(result.Collection.Indexes) != 0 {
		t.Fatalf("created SQL view=%+v", result.Collection)
	}
	reloaded, err := NewStore(pool).ReadCatalog(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	stored := reloaded.Collections["sql_totals"]
	if stored.View == nil || stored.View.SQL != sql || len(stored.View.Dependencies) != 2 {
		t.Fatalf("reloaded SQL view=%+v", stored)
	}
	var label string
	var total int64
	var epoch bool
	if err := pool.QueryRow(ctx, `select label,total,created_at='epoch'::timestamptz and updated_at='epoch'::timestamptz from app.sql_totals where category='a'`).Scan(&label, &total, &epoch); err != nil {
		t.Fatal(err)
	}
	if label != "Alpha" || total != 5 || !epoch {
		t.Fatalf("joined aggregate label=%q total=%d epoch=%v", label, total, epoch)
	}
	updatedSQL := `select (array_agg(i.id order by i.id))[1] as id, i.category, upper(l.label) as label, sum(i.amount)::bigint as total
		from app.sql_items i join app.sql_labels l on l.category=i.category group by i.category,l.label`
	update := NewUpdateChange(UpdateCollectionInput{Name: "sql_totals", View: &ViewInput{SQL: updatedSQL}})
	updatePlan, err := service.Preview(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Apply(ctx, LegacyApply(update, updatePlan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Collection.Fields[0].ID != stored.Fields[0].ID || updated.Collection.View.SQL != updatedSQL {
		t.Fatalf("updated SQL view=%+v", updated.Collection)
	}
	if err := pool.QueryRow(ctx, `select label from app.sql_totals where category='a'`).Scan(&label); err != nil || label != "ALPHA" {
		t.Fatalf("updated SQL result label=%q err=%v", label, err)
	}
	for _, guarded := range []Change{
		NewUpdateChange(UpdateCollectionInput{Name: "sql_items", Fields: []FieldInput{{Name: "category", Type: TypeText}, {Name: "amount", Type: TypeBigInt}}}),
		NewDeleteChange("sql_labels"),
	} {
		_, err := service.Preview(ctx, guarded)
		var detail *ValidationError
		if !errors.As(err, &detail) {
			t.Fatalf("dependency guard error=%v", err)
		}
	}
	if items.ID == labels.ID {
		t.Fatal("source fixture IDs collided")
	}
}
