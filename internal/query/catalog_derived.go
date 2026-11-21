package query

import (
	"sync"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// catalogDerived holds read-path metadata derived from one immutable catalog.
// It is stored in the catalog's own shared computation slot (schema.Catalog
// ReverseExpand), so its lifetime is exactly that compiled catalog's: a new
// schema version, a re-read of the same version, or any caller-owned copy gets
// a fresh value, and nothing is keyed by the catalog version number (which
// the schema cache itself does not treat as a content identity). On a catalog
// without that slot every call builds a fresh, private value, which is the
// former per-request behaviour.
//
// Each member is computed lazily from the catalog passed by the caller; that
// catalog shares its maps with the one the slot belongs to.
type catalogDerived struct {
	reverseOnce sync.Once
	reverse     reverseExpandIndex
	reverseErr  error

	byIDOnce sync.Once
	byID     map[uuid.UUID]schema.Collection

	digests sync.Map // collection ID -> [32]byte
}

func derivedCatalog(catalog schema.Catalog) *catalogDerived {
	value, _ := catalog.ReverseExpand(func(schema.Catalog) (any, error) {
		return &catalogDerived{}, nil
	})
	return value.(*catalogDerived)
}

func (d *catalogDerived) reverseExpandIndex(catalog schema.Catalog) (reverseExpandIndex, error) {
	d.reverseOnce.Do(func() { d.reverse, d.reverseErr = buildReverseExpandIndex(catalog) })
	return d.reverse, d.reverseErr
}

// collectionByID admits an ID only when exactly one collection, stored under
// its own name, carries it — the rule the former linear scan applied.
func (d *catalogDerived) collectionByID(catalog schema.Catalog, id uuid.UUID) (schema.Collection, bool) {
	d.byIDOnce.Do(func() {
		matches := make(map[uuid.UUID]int, len(catalog.Collections))
		found := make(map[uuid.UUID]schema.Collection, len(catalog.Collections))
		for name, collection := range catalog.Collections {
			if name != collection.Name {
				continue
			}
			matches[collection.ID]++
			found[collection.ID] = collection
		}
		for collectionID, count := range matches {
			if count != 1 {
				delete(found, collectionID)
			}
		}
		d.byID = found
	})
	collection, ok := d.byID[id]
	return collection, ok
}

// schemaDigest returns recordartifact.SchemaDigest for a collection taken
// from this catalog. Errors are not cached; the digest is a pure function of
// the immutable collection, so a cached success equals a recomputation.
func (d *catalogDerived) schemaDigest(collection schema.Collection) ([32]byte, error) {
	if cached, ok := d.digests.Load(collection.ID); ok {
		return cached.([32]byte), nil
	}
	digest, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		return [32]byte{}, err
	}
	d.digests.Store(collection.ID, digest)
	return digest, nil
}
