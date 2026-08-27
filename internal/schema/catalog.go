package schema

// Catalog is the authoritative collection metadata published to request readers.
type Catalog struct {
	Version     int64
	Collections map[string]Collection
}

func cloneCatalog(source Catalog) Catalog {
	clone := Catalog{Version: source.Version, Collections: make(map[string]Collection, len(source.Collections))}
	for name, collection := range source.Collections {
		collection.Fields = append([]Field(nil), collection.Fields...)
		for index := range collection.Fields {
			collection.Fields[index].Default = append([]byte(nil), collection.Fields[index].Default...)
		}
		clone.Collections[name] = collection
	}
	return clone
}
