package recordops

import (
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestDeleteClosureGroupsUseDeterministicCollectionUUIDOrder(t *testing.T) {
	rootCollection := schema.Collection{ID: uuid.MustParse("f0000000-0000-4000-8000-000000000001"), Name: "root"}
	derivedCollection := schema.Collection{ID: uuid.MustParse("10000000-0000-4000-8000-000000000001"), Name: "derived"}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{
		rootCollection.Name: rootCollection, derivedCollection.Name: derivedCollection,
	}}
	root := relation.RecordRef{CollectionID: rootCollection.ID, RecordID: uuid.MustParse("f0000000-0000-4000-8000-000000000002")}
	derived := relation.RecordRef{CollectionID: derivedCollection.ID, RecordID: uuid.MustParse("10000000-0000-4000-8000-000000000002")}
	groups, err := groupDeleteClosure(catalog, root, []relation.RecordRef{derived, root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].collection.ID != derivedCollection.ID || groups[1].collection.ID != rootCollection.ID {
		t.Fatalf("group order = %#v, want derived UUID then root UUID", groups)
	}
}
