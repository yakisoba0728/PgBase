package recordops

import (
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
)

func TestClosurePublicOutboxUsesPhysicalKeysAndPublicValues(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{{Name: "renamed", ColumnName: "original", Type: schema.TypeText}, {Name: "links", ColumnName: "old_links", Type: schema.TypeRelation}}}
	public := publicOutboxRecord(collection, map[string]any{"id": "TextId", "renamed": "value", "links": []string{"TargetOne"}})
	if public["id"] != "TextId" || public["original"] != "value" || public["renamed"] != nil || public["old_links"].([]string)[0] != "TargetOne" {
		t.Fatalf("snapshot=%#v", public)
	}
}
