//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrationSnapshotReplayPreservesMetadataAndAdvancedIndexes(t *testing.T) {
	ctx := context.Background()
	pool, service, actor := newSchemaServiceTest(t)
	yes := true
	created := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "snapshot_items", CompatibilityName: "Snapshot_Items", System: true, ValidationMode: ValidationModePocketBase,
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, System: &yes},
			{Name: "score", Type: TypeBigInt, Required: true},
		},
		Indexes: []IndexInput{
			{
				Name: "snapshot_semantic", Unique: true,
				Keys: []IndexKeyInput{{Expression: "lower(title)", Collation: "C", Order: "desc"}, {Field: "score"}}, Where: "score > 0",
			},
			{Name: "snapshot_raw", RawSQL: `CREATE INDEX snapshot_raw ON snapshot_items ((upper(title))) WHERE score >= 0`},
		},
	}, actor)
	before, err := service.ExportMigrationSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot MigrationSnapshot
	if err := json.Unmarshal(before, &snapshot); err != nil {
		t.Fatal(err)
	}
	item := &snapshot.Collections[0]
	if item.CompatibilityName == nil || item.Type == nil || item.System == nil || item.ValidationMode == nil || item.Fields[0].System == nil {
		t.Fatalf("exported snapshot omitted metadata: %+v", item)
	}
	if !snapshotIndexesComplete(item.Indexes) {
		t.Fatalf("exported snapshot flattened advanced index: %+v", item.Indexes)
	}
	item.Fields[1].Help = "changed by snapshot replay"
	after, err := MarshalMigrationSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return ApplyMigrationSnapshot(ctx, tx, before, after)
	}); err != nil {
		t.Fatalf("apply snapshot: %#v", err)
	}
	store := NewStore(pool)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.ReadCatalog(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	got := current.Collections[created.Name]
	if got.CompatibilityName != "Snapshot_Items" || !got.System || got.ValidationMode != ValidationModePocketBase || !got.Fields[0].System {
		t.Fatalf("metadata after replay: %+v fields=%+v", got, got.Fields)
	}
	if !catalogIndexesComplete(got.Indexes) {
		t.Fatalf("advanced index after replay: %+v", got.Indexes)
	}
}

func snapshotIndexesComplete(indexes []IndexInput) bool {
	var structured, raw bool
	for _, index := range indexes {
		structured = structured || len(index.Keys) != 0 && index.Where != ""
		raw = raw || index.RawSQL != ""
	}
	return len(indexes) == 2 && structured && raw
}

func catalogIndexesComplete(indexes []Index) bool {
	var structured, raw bool
	for _, index := range indexes {
		structured = structured || len(index.Keys) != 0 && index.Where != ""
		raw = raw || index.RawSQL != ""
	}
	return len(indexes) == 2 && structured && raw
}

func TestLegacyMigrationSnapshotReplayInheritsAbsentMetadata(t *testing.T) {
	ctx := context.Background()
	pool, service, actor := newSchemaServiceTest(t)
	yes := true
	created := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "legacy_snapshot", CompatibilityName: "Legacy_Snapshot", System: true, ValidationMode: ValidationModePocketBase,
		Fields: []FieldInput{{Name: "title", Type: TypeText, System: &yes}},
	}, actor)
	current, err := service.ExportMigrationSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var legacy MigrationSnapshot
	if err := json.Unmarshal(current, &legacy); err != nil {
		t.Fatal(err)
	}
	item := &legacy.Collections[0]
	item.CompatibilityName, item.Type, item.System, item.ValidationMode = nil, nil, nil, nil
	item.Fields[0].System = nil
	before, err := MarshalMigrationSnapshot(legacy)
	if err != nil {
		t.Fatal(err)
	}
	item.Fields[0].Help = "legacy update"
	after, err := MarshalMigrationSnapshot(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return ApplyMigrationSnapshot(ctx, tx, before, after)
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Collections[created.Name]
	if got.CompatibilityName != "Legacy_Snapshot" || !got.System || got.ValidationMode != ValidationModePocketBase || !got.Fields[0].System || got.Fields[0].Help != "legacy update" {
		t.Fatalf("legacy replay lost metadata: %+v fields=%+v", got, got.Fields)
	}
}
