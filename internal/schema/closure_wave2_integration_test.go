//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

func TestClosureArithmeticIndexAndNestedViewRename(t *testing.T) {
	_, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "closure_math", Fields: []FieldInput{{Name: "score", Type: TypeInteger}}, Indexes: []IndexInput{{Name: "plus_one", Keys: []IndexKeyInput{{Expression: "score + 1"}}}}}, actor)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "closure_vone", View: &ViewInput{SQL: `select id,score from app.closure_math`}}, actor)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "closure_vtwo", View: &ViewInput{SQL: `select id,score from app.closure_vone`}}, actor)
	change := NewUpdateChange(UpdateCollectionInput{Name: "closure_renamed", CollectionID: &created.ID})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, LegacyApply(change, plan, actor)); err != nil {
		t.Fatal(err)
	}
}

func TestClosureCompanionCapabilityAcrossImportAndSnapshot(t *testing.T) {
	for _, mode := range []string{"import", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			pool, service, actor, _ := companionRolloutFixture(t)
			ctx := context.Background()
			activateCompanionRollout(t, service)
			if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
				t.Fatal(err)
			}
			for _, gap := range []bool{false, true} {
				if mode == "import" {
					doc, err := service.ExportSchema(ctx)
					if err != nil {
						t.Fatal(err)
					}
					doc.AuthCollections = nil
					if gap {
						doc.Collections[0].Fields = append(doc.Collections[0].Fields, FieldInput{Name: "added", Type: TypeInteger, Indexed: true})
					} else {
						doc.Collections[0].Fields[0].Help = "new help"
					}
					plan, err := service.PreviewSchemaImport(ctx, doc)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: doc, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "closure-companion-" + uuid.NewString(), ValidateRules: acceptMigrationRulesForTest}); err != nil {
						t.Fatal(err)
					}
				} else {
					before, err := service.ExportMigrationSnapshot(ctx)
					if err != nil {
						t.Fatal(err)
					}
					var snap MigrationSnapshot
					if err = json.Unmarshal(before, &snap); err != nil {
						t.Fatal(err)
					}
					if gap {
						id := uuid.New()
						snap.Collections[0].Fields = append(snap.Collections[0].Fields, FieldInput{ID: &id, Name: "added", Type: TypeInteger, Indexed: true})
					} else {
						snap.Collections[0].Fields[0].Help = "new help"
					}
					after, err := MarshalMigrationSnapshot(snap)
					if err != nil {
						t.Fatal(err)
					}
					if err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
						return ApplyMigrationSnapshot(ctx, tx, before, after, acceptMigrationRulesForTest)
					}); err != nil {
						t.Fatal(err)
					}
				}
				var state string
				var version, stored, count int64
				if err := pool.QueryRow(ctx, `select c.state,s.version,coalesce(c.catalog_version,0),coalesce(c.ready_index_count,0) from pgbase_meta.schema_companion_index_capability c cross join pgbase_meta.schema_state s where c.singleton and s.singleton`).Scan(&state, &version, &stored, &count); err != nil {
					t.Fatal(err)
				}
				if gap {
					if state == "ready_v3" {
						t.Fatalf("new companion left ready: %s/%d/%d/%d", state, version, stored, count)
					}
				} else if state != "ready_v3" || stored != version || count != 1 {
					t.Fatalf("metadata capability stale: %s/%d/%d/%d", state, version, stored, count)
				}
			}
		})
	}
}

func TestClosureUnsupportedAuthImportRejectedByPreviewAndApply(t *testing.T) {
	_, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	value := schemaDocumentAuthCollectionForTest()
	value.Name = "closure_bad_auth"
	value.Fields = []FieldInput{{Name: "custom", Type: "nonsense", Required: true}}
	doc := SchemaDocument{Format: SchemaDocumentFormat, AuthCollections: []SchemaDocumentAuthCollection{value}}
	check := func(err error) {
		t.Helper()
		var invalid *ValidationError
		if !errors.As(err, &invalid) || len(invalid.Details) != 1 || invalid.Details[0].Field != "authCollections.closure_bad_auth.fields" {
			t.Fatalf("wrong boundary error: %#v", err)
		}
	}
	_, err := service.PreviewSchemaImport(ctx, doc)
	check(err)
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: doc, BaseVersion: catalog.Version, Checksum: strings.Repeat("0", 64), Actor: actor, OperationID: uuid.New(), IdempotencyKey: "closure-invalid-auth-import"})
	check(err)
}
