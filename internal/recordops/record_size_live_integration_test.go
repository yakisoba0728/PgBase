//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/webisnoteasy/PgBase/internal/schema"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestCreateRefusesRecordOverThePageBudget(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()

	_, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "size-create-0001", Collection: "records",
		IdempotencyKey: "size-create-key-00001",
		Input: map[string]json.RawMessage{
			"title":    json.RawMessage(`"oversized"`),
			"metadata": jsonStringOfSize(maximumRecordBytes),
		},
	})
	assertQueryValidation(t, err, "record", "record_too_large")

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	page, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "size-list-000001", Collection: "records", Limit: 10,
	})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("list after refused create = %#v, %v; want an empty page", page.Items, err)
	}

	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "size-create-0002", Collection: "records",
		IdempotencyKey: "size-create-key-00002",
		Input: map[string]json.RawMessage{
			"title":    json.RawMessage(`"bounded"`),
			"metadata": jsonStringOfSize(1 << 20),
		},
	})
	if err != nil {
		t.Fatalf("create bounded record: %v", err)
	}
	page, err = records.List(ctx, ListRequest{
		Principal: principal, RequestID: "size-list-000002", Collection: "records", Limit: 10,
	})
	if err != nil {
		t.Fatalf("list bounded record: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0]["id"] != created.Record.Data["id"] {
		t.Fatalf("list after bounded create = %d items, want the created record", len(page.Items))
	}
}

func TestUpdateRefusesGrowingARecordOverThePageBudget(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()

	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "grow-create-0001", Collection: "records",
		IdempotencyKey: "grow-create-key-00001",
		Input: map[string]json.RawMessage{
			"title":    json.RawMessage(`"growing"`),
			"metadata": jsonStringOfSize(1 << 20),
		},
	})
	if err != nil {
		t.Fatalf("create record: %v", err)
	}
	recordID := uuid.MustParse(created.Record.Data["id"].(string))

	// Every field stays inside the per-field bounds package query enforces on
	// input; only the merged record crosses the page budget.
	_, err = records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "grow-update-0001", Collection: "records", ID: recordID,
		IfMatch: EntityTagSet{Present: true, Values: []string{created.Record.ETag}},
		Input:   map[string]json.RawMessage{"metadata": jsonStringOfSize(maximumRecordBytes)},
	})
	assertQueryValidation(t, err, "record", "record_too_large")

	got, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "grow-get-0000001", Collection: "records", ID: recordID,
	})
	if err != nil {
		t.Fatalf("get after refused update: %v", err)
	}
	if got.ETag != created.Record.ETag || got.Version != created.Record.Version {
		t.Fatalf("refused update changed the revision: %#v, want %#v", got, created.Record)
	}
	before, _ := json.Marshal(created.Record.Data["metadata"])
	after, _ := json.Marshal(got.Data["metadata"])
	if string(before) != string(after) {
		t.Fatalf("refused update changed metadata: %d bytes, want %d", len(after), len(before))
	}
}

// TestLegacyOversizedRecordBlocksListButStaysShrinkable covers the data a
// write-time bound cannot retroactively fix. A record an older build stored
// above the list page budget still fails the page it lands on, and the point
// of exempting the PATCH pre-image is that shrinking it repairs the collection
// without a manual SQL edit.
func TestLegacyOversizedRecordBlocksListButStaysShrinkable(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()

	legacyID := uuid.Must(uuid.NewV7())
	// Above the 4 MiB read budget, not merely above the 2 MiB write bound, so
	// the record is genuinely unreachable through list.
	payload := jsonStringOfSize(5 << 20)
	if _, err := pool.Exec(ctx, `INSERT INTO app.records (id, created_at, updated_at, title, count, metadata)
		VALUES ($1, transaction_timestamp(), transaction_timestamp(), 'legacy', 0, $2::jsonb)`,
		legacyID, string(payload)); err != nil {
		t.Fatalf("plant legacy oversized record: %v", err)
	}

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	_, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "legacy-list-0001", Collection: "records", Limit: 10,
	})
	assertQueryValidation(t, err, "response", "response_too_large")

	// A drop-only schema change can be one step of a legacy repair, even when
	// removing one field does not yet bring the row under the write bound.
	change := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "records", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true, Unique: true},
		{Name: "metadata", Type: schema.TypeJSONB},
	}})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID)); err != nil {
		t.Fatalf("drop field during legacy repair: %v", err)
	}

	if _, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "legacy-shrink-01", Collection: "records", ID: legacyID,
		Input: map[string]json.RawMessage{"metadata": json.RawMessage(`"shrunk"`)},
	}); err != nil {
		t.Fatalf("shrink legacy oversized record: %v", err)
	}

	page, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "legacy-list-0002", Collection: "records", Limit: 10,
	})
	if err != nil {
		t.Fatalf("list after shrinking the legacy record: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0]["id"] != legacyID.String() {
		t.Fatalf("list after shrink = %#v, want the repaired record", page.Items)
	}
}

// Each apply fits the HTTP request budget. Defaults materialize in existing
// rows even when a later apply removes the default from the catalog.
func TestSchemaDefaultsCannotGrowExistingRecordsPastTheWriteLimit(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	testutil.RetireRecordMembershipV4(t, ctx, pool)
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "schema-size-create", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"bounded"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(created.Record.Data["id"].(string))
	fields := []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true, Unique: true},
		{Name: "count", Type: schema.TypeInteger},
		{Name: "metadata", Type: schema.TypeJSONB},
	}
	payload, _ := json.Marshal(strings.Repeat("x", 256<<10))
	for index := 0; index < 8; index++ {
		for f := range fields {
			fields[f].Default = nil
		}
		fields = append(fields, schema.FieldInput{Name: fmt.Sprintf("extra_%d", index), Type: schema.TypeJSONB, Default: payload})
		change := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "records", Fields: fields})
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		_, err = schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID))
		if index < 7 {
			if err != nil {
				t.Fatalf("bounded apply %d: %v", index, err)
			}
			continue
		}
		assertQueryValidation(t, err, "record", "record_too_large")
		catalog, err := schemas.Catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if catalog.Version != plan.BaseVersion || len(catalog.Collections["records"].Fields) != len(fields)-1 {
			t.Fatal("refused apply changed the catalog")
		}
	}
	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "schema-size-get", Collection: "records", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got.Data["extra_7"]; exists {
		t.Fatal("refused column survived rollback")
	}
	if got.Data["extra_6"] == nil {
		t.Fatal("previously committed default disappeared")
	}
	if _, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "schema-size-update", Collection: "records", ID: id,
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"still writable"`)},
	}); err != nil {
		t.Fatalf("patch after rejected apply: %v", err)
	}
	page, err := records.List(ctx, ListRequest{Principal: principal, RequestID: "schema-size-list", Collection: "records", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list after rejected apply: %d records, %v", len(page.Items), err)
	}
}
