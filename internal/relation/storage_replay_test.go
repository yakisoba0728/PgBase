package relation

import (
	"fmt"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/schema"
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
