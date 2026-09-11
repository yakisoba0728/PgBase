package responseprojection

import "testing"

func TestPocketBaseRecordAddsMetadataWithoutMutatingNativeRecord(t *testing.T) {
	native := map[string]any{"id": "uuid", "created_at": "created", "updated_at": "updated"}
	compatible := PocketBaseRecord(native, "collection-uuid", "posts")
	if compatible["collectionId"] != "collection-uuid" || compatible["collectionName"] != "posts" || compatible["created"] != "created" || compatible["updated"] != "updated" {
		t.Fatalf("record=%#v", compatible)
	}
	if _, changed := native["collectionId"]; changed {
		t.Fatalf("native mutated=%#v", native)
	}
}
