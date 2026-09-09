//go:build integration

package schema

import (
	"context"
	"testing"
)

func TestFieldPresentationMetadataRoundTripsThroughPostgres(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	wantHelp := "Shown below the editor and carried by schema export."
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "presentation_fields", Fields: []FieldInput{
		{Name: "display_name", Type: TypeText, Presentable: true, Help: wantHelp},
		{Name: "internal_note", Type: TypeText, Hidden: true},
	}}, admin)
	if !created.Fields[0].Presentable || created.Fields[0].Help != wantHelp || !created.Fields[1].Hidden {
		t.Fatalf("apply metadata = %#v", created.Fields)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stored := catalog.Collections[created.Name]
	if !stored.Fields[0].Presentable || stored.Fields[0].Help != wantHelp || !stored.Fields[1].Hidden {
		t.Fatalf("stored metadata = %#v", stored.Fields)
	}
}
