package realtime

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
)

func TestPublicSnapshotMapsPhysicalNamesAndDropsHiddenFields(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "articles", Fields: []schema.Field{{Name: "title", ColumnName: "fld_title"}, {Name: "secret", ColumnName: "fld_secret", Hidden: true}}}
	record, err := publicSnapshot(collection, []byte(`{"id":"018f3b2e-4200-7000-8000-000000000001","created_at":"2026-09-10T00:00:00Z","updated_at":"2026-09-10T00:00:00Z","fld_title":"shown","fld_secret":"hidden"}`))
	if err != nil {
		t.Fatal(err)
	}
	if record["title"] != "shown" || record["created"] == nil || record["collectionName"] != "articles" {
		t.Fatalf("record=%v", record)
	}
	if _, ok := record["secret"]; ok {
		t.Fatalf("hidden field leaked: %v", record)
	}
}
