//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestEditorAndGeoPointPhysicalDefaultsAndPortableMetadata(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	maximum := int64(5 << 20)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "advanced_values", Fields: []FieldInput{
		{Name: "body", Type: TypeEditor, Options: &FieldOptions{MaxSize: &maximum, ConvertURLs: true}},
		{Name: "location", Type: TypeGeoPoint, Indexed: true},
	}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.advanced_values(id,created_at,updated_at) values(gen_random_uuid(),now(),now())`); err != nil {
		t.Fatal(err)
	}
	var body string
	var point []byte
	if err := pool.QueryRow(ctx, `select body,location from app.advanced_values`).Scan(&body, &point); err != nil {
		t.Fatal(err)
	}
	if body != "" || string(point) != `{"lat": 0, "lon": 0}` && string(point) != `{"lat":0,"lon":0}` {
		t.Fatalf("defaults body=%q point=%s", body, point)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fields := catalog.Collections[created.Name].Fields
	if fields[0].Type != TypeEditor || fields[0].Options.MaxSize == nil || *fields[0].Options.MaxSize != maximum || !fields[0].Options.ConvertURLs || fields[1].Type != TypeGeoPoint {
		encoded, _ := json.Marshal(fields)
		t.Fatalf("catalog fields=%s", encoded)
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if document.Collections[0].Fields[0].Type != TypeEditor || document.Collections[0].Fields[1].Type != TypeGeoPoint {
		t.Fatalf("document=%+v", document)
	}
	_, target, targetAdmin := newSchemaServiceTest(t)
	plan, err := target.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: targetAdmin, OperationID: uuid.New(), IdempotencyKey: "advanced-fields-import-0001"}); err != nil {
		t.Fatal(err)
	}
}
