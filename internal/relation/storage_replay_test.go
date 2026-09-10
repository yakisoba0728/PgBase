package relation

import (
	"fmt"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestReplayHydrationMaximumWidthProjectionFitsPostgresTargetList(t *testing.T) {
	const maximumCollectionFields = 1_597
	fields := make([]schema.Field, maximumCollectionFields)
	for ordinal := range maximumCollectionFields - 1 {
		name := fmt.Sprintf("field_%04d", ordinal)
		fields[ordinal] = schema.Field{Name: name, ColumnName: name, Type: schema.TypeText, Ordinal: ordinal}
	}
	fields[maximumCollectionFields-1] = schema.Field{
		Name: "links", ColumnName: "links", Type: schema.TypeRelation, Ordinal: maximumCollectionFields - 1,
		Relation: &schema.RelationSpec{Cardinality: schema.RelationMultiple},
	}

	bindings := &Bindings{sourceFields: fields}
	const replayMetadataColumns = 5
	if got := len(bindings.replayRootColumns("replay_root")) + replayMetadataColumns; got != 1_604 {
		t.Fatalf("maximum-width replay result columns = %d, want 1604 (below PostgreSQL's 1664 target-list limit)", got)
	}
}

func TestReplayHydrationRootProjectionSkipsFileAndMultipleRelationFields(t *testing.T) {
	bindings := &Bindings{sourceFields: []schema.Field{
		{Name: "title", ColumnName: "title", Type: schema.TypeText, Ordinal: 0},
		{Name: "attachment", ColumnName: "attachment", Type: schema.TypeFile, Ordinal: 1},
		{Name: "tags", ColumnName: "tags", Type: schema.TypeRelation, Ordinal: 2, Relation: &schema.RelationSpec{Cardinality: schema.RelationMultiple}},
	}}

	got := bindings.replayRootColumns("replay_root")
	want := []string{
		`"replay_root"."id"`, `"replay_root"."created_at"`, `"replay_root"."updated_at"`, `"replay_root"."title"`,
	}
	if len(got) != len(want) {
		t.Fatalf("replay root columns = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("replay root column %d = %q, want %q", index, got[index], want[index])
		}
	}
}
