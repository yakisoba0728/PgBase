package schema

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const maximumCatalogWireBytes = 16 << 20

var ErrCatalogLimitExceeded = errors.New("catalog exceeds the serialized size limit")

type Catalog struct {
	Version     int64                       `json:"version"`
	Collections map[string]Collection       `json:"collections"`
	Incoming    map[uuid.UUID][]RelationRef `json:"-"`
	wire        []byte
	deleteGraph *catalogDeleteGraph
}

// catalogDeleteGraph is the delete graph one catalog version shares with every
// request pinned to it. schema cannot import the package that owns the type, so
// the compiled value crosses that boundary as any. A failure is cached with the
// value because the input is immutable: a second attempt would fail identically.
type catalogDeleteGraph struct {
	once  sync.Once
	value any
	err   error
}

// DeleteGraph runs build at most once per catalog version and returns the
// result every later caller shares. Compilation is deferred because a process
// that never deletes never needs it. Only a compiled snapshot carries the
// shared slot; a caller-owned copy may be mutated, so it builds its own graph.
func (c Catalog) DeleteGraph(build func(Catalog) (any, error)) (any, error) {
	if c.deleteGraph == nil {
		return build(c)
	}
	c.deleteGraph.once.Do(func() { c.deleteGraph.value, c.deleteGraph.err = build(c) })
	return c.deleteGraph.value, c.deleteGraph.err
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
	immutable.deleteGraph = &catalogDeleteGraph{}
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
		renames := map[string]string{}
		oldByID := map[uuid.UUID]string{}
		for _, field := range current.Fields {
			oldByID[field.ID] = field.Name
		}
		for _, field := range fields {
			if old := oldByID[field.ID]; old != "" && old != field.Name {
				renames[old] = field.Name
			}
		}
		current.Fields = fields
		indexes, err := updatedIndexMetadata(current.Name, current.Indexes, plan.Definition)
		if err != nil {
			return Catalog{}, err
		}
		current.Indexes = indexes
		if plan.Definition.View != nil {
			current.View = resolvedViewSpec(plan.Definition.View)
		}
		next.Collections[current.Name] = current
		for name, dependent := range next.Collections {
			if dependent.View == nil || dependent.View.SourceCollectionID != current.ID {
				continue
			}
			for i, value := range dependent.View.Projection {
				if renamed := renames[value]; renamed != "" {
					dependent.View.Projection[i] = renamed
				}
			}
			renameViewFilterFields(dependent.View.Filter, renames)
			for i, field := range dependent.Fields {
				if renamed := renames[field.Name]; renamed != "" {
					dependent.Fields[i].Name = renamed
				}
			}
			next.Collections[name] = dependent
		}
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

func renameViewFilterFields(filter *ViewFilter, renames map[string]string) {
	if filter == nil {
		return
	}
	if renamed := renames[filter.Field]; renamed != "" {
		filter.Field = renamed
	}
	for i := range filter.Args {
		renameViewFilterFields(&filter.Args[i], renames)
	}
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
			collection.Fields[index].Relation = clonePointer(collection.Fields[index].Relation)
			collection.Fields[index].Options = cloneFieldOptions(collection.Fields[index].Options)
		}
		collection.Indexes = append([]Index(nil), collection.Indexes...)
		for index := range collection.Indexes {
			collection.Indexes[index].Fields = append([]string(nil), collection.Indexes[index].Fields...)
		}
		if collection.View != nil {
			view := *collection.View
			view.Projection = append([]string(nil), collection.View.Projection...)
			view.Filter = cloneViewFilter(collection.View.Filter)
			view.Dependencies = append([]uuid.UUID(nil), collection.View.Dependencies...)
			collection.View = &view
		}
		clone.Collections[name] = collection
	}
	for targetCollectionID, incoming := range source.Incoming {
		clone.Incoming[targetCollectionID] = append([]RelationRef(nil), incoming...)
	}
	return clone
}
