//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCompatibilityMetadataPersistsExportsAndProtectsSystemField(t *testing.T) {
	ctx := context.Background()
	_, service, admin := newSchemaServiceTest(t)
	yes := true
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "compat_items", CompatibilityName: "Compat_Items", Type: CollectionTypeBase, System: true, ValidationMode: ValidationModePocketBase, Fields: []FieldInput{{Name: "title", Type: TypeText, System: &yes}}}, admin)
	if created.CompatibilityName != "Compat_Items" || created.Type != CollectionTypeBase || !created.System || created.ValidationMode != ValidationModePocketBase || !created.Fields[0].System {
		t.Fatalf("created metadata=%#v", created)
	}
	reloaded, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Collections[created.Name]
	if got.CompatibilityName != "Compat_Items" || !got.Fields[0].System {
		t.Fatalf("reloaded=%#v", got)
	}
	doc, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(doc)
	if !json.Valid(encoded) || doc.Collections[0].ValidationMode != ValidationModePocketBase || doc.Collections[0].Fields[0].System == nil || !*doc.Collections[0].Fields[0].System {
		t.Fatalf("export=%s", encoded)
	}
	id := created.Fields[0].ID
	change := NewUpdateChange(UpdateCollectionInput{Name: created.Name, Fields: []FieldInput{{ID: &id, Name: "renamed", Type: TypeText, System: &yes}}})
	_, err = service.Preview(ctx, change)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("system rename error=%v", err)
	}
	_, err = service.Preview(ctx, NewDeleteChange(created.Name))
	if !errors.As(err, &validationErr) {
		t.Fatalf("system delete error=%v", err)
	}
}

func TestDeclarativeViewUpdateAndDryRunSample(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "sample_source", Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "score", Type: TypeInteger}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.sample_source(id,created_at,updated_at,title,score) values(gen_random_uuid(),now(),now(),'one',1)`); err != nil {
		t.Fatal(err)
	}
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "sample_view", View: &ViewInput{Source: "sample_source", Projection: []string{"title"}}}, admin)
	updated := applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: view.Name, View: &ViewInput{Source: "sample_source", Projection: []string{"score"}}}), admin).Collection
	if len(updated.Fields) != 1 || updated.Fields[0].Name != "score" {
		t.Fatalf("updated view=%#v", updated)
	}
	sample, err := service.SampleView(ctx, `select id,title from app.sample_source`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sample.Fields) != 1 || sample.Fields[0].Name != "title" || len(sample.Items) != 1 || len(sample.Dependencies) != 1 {
		t.Fatalf("sample=%#v", sample)
	}
}

func TestPocketBaseModeAndDateJSONTighteningRejectExistingRows(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "tighten_compat", Fields: []FieldInput{{Name: "enabled", Type: TypeBoolean, Required: true, Default: json.RawMessage(`true`)}, {Name: "happened_at", Type: TypeTimestampTZ}, {Name: "data", Type: TypeJSONB}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.tighten_compat(id,created_at,updated_at,enabled,"happened_at",data) values(gen_random_uuid(),now(),now(),false,'2025-01-01T00:00:00Z','{"long":true}')`); err != nil {
		t.Fatal(err)
	}
	inputs := make([]FieldInput, len(created.Fields))
	for i, f := range created.Fields {
		id := f.ID
		inputs[i] = FieldInput{ID: &id, Name: f.Name, Type: f.Type, Required: f.Required, Default: f.Default}
	}
	mode := ValidationModePocketBase
	change := NewUpdateChange(UpdateCollectionInput{Name: created.Name, ValidationMode: &mode, Fields: inputs})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, LegacyApply(change, plan, admin)); !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("PB tightening error=%v", err)
	}
	minimum := "2026-01-01T00:00:00Z"
	inputs[1].Options = &FieldOptions{Min: &minimum}
	change = NewUpdateChange(UpdateCollectionInput{Name: created.Name, Fields: inputs})
	plan, err = service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, LegacyApply(change, plan, admin)); !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("date tightening error=%v", err)
	}
	inputs[1].Options = nil
	maximum := int64(2)
	inputs[2].Options = &FieldOptions{MaxSize: &maximum}
	change = NewUpdateChange(UpdateCollectionInput{Name: created.Name, Fields: inputs})
	plan, err = service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, LegacyApply(change, plan, admin)); !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("JSON tightening error=%v", err)
	}
}
