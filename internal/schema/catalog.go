package schema

import "github.com/google/uuid"

// Catalog is the authoritative collection metadata published to request readers.
type Catalog struct {
	Version     int64
	Collections map[string]Collection
	Incoming    map[uuid.UUID][]RelationRef
}

func cloneCatalog(source Catalog) Catalog {
	clone := Catalog{
		Version:     source.Version,
		Collections: make(map[string]Collection, len(source.Collections)),
		Incoming:    make(map[uuid.UUID][]RelationRef, len(source.Incoming)),
	}
	for name, collection := range source.Collections {
		collection.Fields = append([]Field(nil), collection.Fields...)
		for index := range collection.Fields {
			collection.Fields[index].Default = append([]byte(nil), collection.Fields[index].Default...)
			collection.Fields[index].Relation = cloneRelationSpec(collection.Fields[index].Relation)
		}
		clone.Collections[name] = collection
	}
	for targetCollectionID, incoming := range source.Incoming {
		clone.Incoming[targetCollectionID] = append([]RelationRef(nil), incoming...)
	}
	return clone
}
