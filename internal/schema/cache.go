package schema

import "sync/atomic"

// Cache contains only committed immutable catalogs. Candidates belong to the
// transaction that compiled them and never enter shared state before commit.
type Cache struct {
	current atomic.Pointer[compiledCatalog]
}

// Load returns a caller-owned copy of the currently published catalog.
func (c *Cache) Load() Catalog {
	if c == nil {
		return Catalog{Collections: map[string]Collection{}}
	}
	current := c.current.Load()
	if current == nil {
		return Catalog{Collections: map[string]Collection{}}
	}
	return cloneCatalog(current.catalog)
}

func (c *Cache) find(version int64) *compiledCatalog {
	if c != nil {
		if current := c.current.Load(); current != nil && current.catalog.Version == version {
			return current
		}
	}
	return nil
}

// prepare returns a transaction-local candidate, reusing only committed data.
func (c *Cache) prepare(catalog Catalog, wireLimit int) (*compiledCatalog, error) {
	if c != nil {
		if current := c.current.Load(); current != nil {
			if current.catalog.Version > catalog.Version {
				return nil, ErrConflict
			}
			if current.catalog.Version == catalog.Version {
				if !sameCatalogContent(catalog, current.catalog) {
					return nil, ErrConflict
				}
				return current, nil
			}
		}
	}
	return compileCatalogWithLimit(catalog, wireLimit)
}

// publish is called only after the owning transaction has committed.
func (c *Cache) publish(compiled *compiledCatalog) {
	if c == nil || compiled == nil {
		return
	}
	for {
		current := c.current.Load()
		if current != nil && current.catalog.Version >= compiled.catalog.Version {
			return
		}
		if c.current.CompareAndSwap(current, compiled) {
			return
		}
	}
}
