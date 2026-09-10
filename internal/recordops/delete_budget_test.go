package recordops

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestChargeDeleteRelationShapeBoundsWideEmptyMaterialization(t *testing.T) {
	fields := make([]schema.Field, 257)
	for index := range fields {
		fields[index] = schema.Field{
			Name:     fmt.Sprintf("relation_%04d", index),
			Type:     schema.TypeRelation,
			Relation: &schema.RelationSpec{Cardinality: schema.RelationMultiple},
		}
	}
	full := func() deleteReadBudget {
		return deleteReadBudget{
			bytes:  maximumDeleteMaterializedBytes,
			rows:   maximumDeleteHydrationRows,
			fields: maximumDeleteHydrationFields,
		}
	}
	assertBudget := func(err error) {
		t.Helper()
		var validation *query.ValidationError
		if !errors.As(err, &validation) || len(validation.Details) != 1 ||
			validation.Details[0].Code != "relation_delete_budget_exceeded" {
			t.Fatalf("budget error = %#v", err)
		}
	}

	budget := full()
	assertBudget(chargeDeleteRelationShape(schema.Collection{Fields: fields}, 1, &budget))

	budget = full()
	assertBudget(chargeDeleteRelationShape(schema.Collection{Fields: fields[:256]}, 10_000, &budget))

	budget = deleteReadBudget{bytes: int64((len(fields[0].Name) + 6) * 2), rows: 1, fields: 1}
	if err := chargeDeleteRelationShape(schema.Collection{Fields: fields[:1]}, 2, &budget); err != nil {
		t.Fatalf("exact boundary: %v", err)
	}
	if budget.bytes != 0 || budget.fields != 0 {
		t.Fatalf("exact boundary remainder = bytes %d, fields %d", budget.bytes, budget.fields)
	}
	budget = deleteReadBudget{bytes: int64((len(fields[0].Name) + 6) * 2), rows: 1, fields: 1}
	assertBudget(chargeDeleteRelationShape(schema.Collection{Fields: fields[:1]}, 3, &budget))
}
