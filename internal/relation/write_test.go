package relation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// stubTx is only ever handed to a Write that has no relation work to do, so a
// statement reaching PostgreSQL through it would panic rather than pass.
type stubTx struct{ pgx.Tx }

func relationFreeCollection() schema.Collection {
	return schema.Collection{
		ID: uuid.MustParse("0199aa22-0000-7000-8000-0000000002ff"), Name: "plain", TableName: "plain",
		Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}},
	}
}

// TestWriteRefusesStorageBeforeLocks pins the ordering the write path depends
// on: relation storage and hydration are only reachable after the collection
// and row locks this Write owns, and only once. Without the guard a caller
// could replace edges under no lock at all.
func TestWriteRefusesStorageBeforeLocks(t *testing.T) {
	write, err := PrepareWrite(schema.Catalog{}, relationFreeCollection(), PatchWrite, map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("PrepareWrite(): %v", err)
	}
	ctx := context.Background()
	sourceID := uuid.MustParse("0199aa22-0000-7000-8000-0000000003ff")
	record := map[string]any{}
	if err := write.Store(ctx, stubTx{}, sourceID, record); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Store before Lock error = %v, want ErrInvalidBinding", err)
	}
	if err := write.Snapshot(ctx, stubTx{}, sourceID, record); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Snapshot before Lock error = %v, want ErrInvalidBinding", err)
	}
	if err := write.Lock(ctx, stubTx{}, sourceID); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if err := write.Store(ctx, stubTx{}, sourceID, record); err != nil {
		t.Fatalf("Store(): %v", err)
	}
	if err := write.Store(ctx, stubTx{}, sourceID, record); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("second Store error = %v, want ErrInvalidBinding", err)
	}
}

func TestPrepareWriteCarriesCanonicalRelationValuesToTheCompiler(t *testing.T) {
	catalog, collection := deleteCatalogFixture()
	target := uuid.MustParse("0199aa22-0000-7000-8000-0000000001aa")
	write, err := PrepareWrite(catalog, collection, PatchWrite, map[string]json.RawMessage{
		"single_null":   json.RawMessage(`"` + target.String() + `"`),
		"cascade_links": json.RawMessage(`["` + target.String() + `"]`),
		"unknown":       json.RawMessage(`1`),
	})
	if err != nil {
		t.Fatalf("PrepareWrite(): %v", err)
	}
	if !write.LocksSource() {
		t.Fatal("a patch that changes relation fields must lock the stored source")
	}
	want := map[string]json.RawMessage{
		"single_null":   json.RawMessage(`"` + target.String() + `"`),
		"cascade_links": json.RawMessage(`["` + target.String() + `"]`),
		"unknown":       json.RawMessage(`1`),
	}
	if !reflect.DeepEqual(write.Scalar(), want) {
		t.Fatalf("Scalar() = %#v, want %#v", write.Scalar(), want)
	}
}

func TestPrepareWriteSkipsBindingForRelationFreeCollections(t *testing.T) {
	// An empty catalog would fail Bind, so reaching a usable write at all
	// proves the relation-free path never binds the catalog.
	write, err := PrepareWrite(schema.Catalog{}, relationFreeCollection(), CreateWrite, map[string]json.RawMessage{
		"title": json.RawMessage(`"value"`),
	})
	if err != nil {
		t.Fatalf("PrepareWrite(): %v", err)
	}
	if write.LocksSource() {
		t.Fatal("a relation-free write must not lock the source row")
	}
	if !reflect.DeepEqual(write.Scalar(), map[string]json.RawMessage{"title": json.RawMessage(`"value"`)}) {
		t.Fatalf("Scalar() = %#v", write.Scalar())
	}
}
