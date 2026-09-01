package relation

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDeleteParticipantOrderMatchesMutationLockDomain(t *testing.T) {
	catalog, collection := deleteCatalogFixture()
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := Bind(catalog, collection)
	if err != nil {
		t.Fatal(err)
	}
	input, err := bindings.Decode(PatchInput, map[string]json.RawMessage{
		"cascade_links": json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.participatingCollections(collection.ID), bindings.collectionLockIDs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("delete/mutation lock domains differ: delete=%v mutation=%v", got, want)
	}
}
