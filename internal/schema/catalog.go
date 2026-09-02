package schema

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

const maximumCatalogWireBytes = 16 << 20

var ErrCatalogLimitExceeded = errors.New("catalog exceeds the serialized size limit")

// Catalog is the authoritative collection metadata published to request readers.
type Catalog struct {
	Version     int64                       `json:"version"`
	Collections map[string]Collection       `json:"collections"`
	Incoming    map[uuid.UUID][]RelationRef `json:"-"`
	wire        []byte
}

type compiledCatalog struct {
	catalog                Catalog
	expectedPhysicalSchema ExpectedPhysicalSchema
}

func compileCatalogWithLimit(source Catalog, wireLimit int) (*compiledCatalog, error) {
	immutable := cloneCatalog(source)
	immutable.wire = nil
	if err := validateCatalogIntegrity(immutable); err != nil {
		return nil, err
	}
	for name, collection := range immutable.Collections {
		collection.CreatedAt = collection.CreatedAt.UTC()
		immutable.Collections[name] = collection
	}
	wire, err := marshalCatalog(immutable)
	if err != nil {
		return nil, err
	}
	if len(wire) > wireLimit {
		return nil, ErrCatalogLimitExceeded
	}
	expectedPhysicalSchema, err := compileExpectedPhysicalSchema(immutable)
	if err != nil {
		return nil, err
	}
	immutable.wire = wire
	return &compiledCatalog{catalog: immutable, expectedPhysicalSchema: expectedPhysicalSchema}, nil
}

func projectCatalog(catalog Catalog, change Change, plan Plan) (Catalog, error) {
	next := cloneCatalog(catalog)
	next.wire = nil
	next.Version = catalog.Version + 1
	switch change.kind {
	case changeCreate:
		collection, err := newCollectionMetadata(plan.Definition)
		if err != nil {
			return Catalog{}, err
		}
		collection.CreatedAt = time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
		next.Collections[collection.Name] = collection
	case changeUpdate:
		current, ok := next.Collections[change.update.Name]
		if !ok {
			return Catalog{}, ErrCollectionNotFound
		}
		fields, err := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
		if err != nil {
			return Catalog{}, err
		}
		current.Fields = fields
		next.Collections[current.Name] = current
	case changeDelete:
		if _, ok := next.Collections[change.deleteName]; !ok {
			return Catalog{}, ErrCollectionNotFound
		}
		delete(next.Collections, change.deleteName)
	default:
		return Catalog{}, ErrConflict
	}
	next.Incoming = buildIncomingIndex(next.Collections)
	return next, nil
}

func buildIncomingIndex(collections map[string]Collection) map[uuid.UUID][]RelationRef {
	incoming := make(map[uuid.UUID][]RelationRef)
	for _, collection := range collections {
		for _, field := range collection.Fields {
			if field.Relation == nil {
				continue
			}
			ref := RelationRef{
				SourceCollectionID: collection.ID,
				SourceFieldID:      field.ID,
				Spec:               *field.Relation,
			}
			incoming[field.Relation.TargetCollectionID] = append(incoming[field.Relation.TargetCollectionID], ref)
		}
	}
	for targetID := range incoming {
		sort.Slice(incoming[targetID], func(i, j int) bool {
			left, right := incoming[targetID][i], incoming[targetID][j]
			if left.SourceCollectionID != right.SourceCollectionID {
				return left.SourceCollectionID.String() < right.SourceCollectionID.String()
			}
			return left.SourceFieldID.String() < right.SourceFieldID.String()
		})
	}
	return incoming
}

func (catalog Catalog) MarshalJSON() ([]byte, error) {
	if catalog.wire != nil {
		return append([]byte(nil), catalog.wire...), nil
	}
	return marshalCatalog(catalog)
}

func marshalCatalog(catalog Catalog) ([]byte, error) {
	collections := catalog.Collections
	if collections == nil {
		collections = map[string]Collection{}
	}
	return json.Marshal(struct {
		Version     int64                 `json:"version"`
		Collections map[string]Collection `json:"collections"`
	}{Version: catalog.Version, Collections: collections})
}

func cloneCatalog(source Catalog) Catalog {
	clone := Catalog{
		Version:     source.Version,
		Collections: make(map[string]Collection, len(source.Collections)),
		Incoming:    make(map[uuid.UUID][]RelationRef, len(source.Incoming)),
		wire:        source.wire,
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
