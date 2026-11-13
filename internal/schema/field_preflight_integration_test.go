//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestTightenedJSONMaximumSizeRejectsEarlierOversizedRow(t *testing.T) {
	for _, mode := range []string{"apply", "import"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			pool, service, admin := newSchemaServiceTest(t)
			service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
			created := applyFreshPlan(t, service, CreateCollectionInput{Name: "json_option_items", Fields: []FieldInput{{Name: "body", Type: TypeJSONB}}}, admin)
			if _, err := pool.Exec(ctx, `insert into app.json_option_items(id,created_at,updated_at,body) values(gen_random_uuid(),now(),now(),'"oversized"'),(gen_random_uuid(),now(),now(),'{}')`); err != nil {
				t.Fatal(err)
			}
			maximum := int64(4)
			options := &FieldOptions{MaxSize: &maximum}
			var err error
			if mode == "apply" {
				id := created.Fields[0].ID
				change := NewUpdateChange(UpdateCollectionInput{Name: created.Name, Fields: []FieldInput{{ID: &id, Name: "body", Type: TypeJSONB, Options: options}}})
				plan, previewErr := service.Preview(ctx, change)
				if previewErr != nil {
					t.Fatal(previewErr)
				}
				_, err = service.Apply(ctx, LegacyApply(change, plan, admin))
			} else {
				document, exportErr := service.ExportSchema(ctx)
				if exportErr != nil {
					t.Fatal(exportErr)
				}
				document.Collections[0].Fields[0].Options = options
				plan, previewErr := service.PreviewSchemaImport(ctx, document)
				if previewErr != nil {
					t.Fatal(previewErr)
				}
				_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "json-size-preflight-import-0001"})
			}
			if !errors.Is(err, ErrDataConstraint) {
				t.Fatalf("%s error=%v, want ErrDataConstraint", mode, err)
			}
			catalog, err := service.Catalog(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if catalog.Version != 1 || catalog.Collections[created.Name].Fields[0].Options.MaxSize != nil {
				t.Fatalf("failed preflight changed catalog: %#v", catalog.Collections[created.Name])
			}
		})
	}
}
