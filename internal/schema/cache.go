package schema

import (
	"sync"
	"sync/atomic"
)

// Cache publishes immutable catalog snapshots atomically.
type Cache struct {
	current   atomic.Pointer[compiledCatalog]
	compileMu sync.Mutex
	prepared  map[int64]*compiledCatalog
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

// find returns a compiled snapshot only for the requested catalog version.
// A prepared snapshot is not globally visible until a transaction that reads
// the same committed schema version proves it is safe to publish.
func (c *Cache) find(version int64) *compiledCatalog {
	if c == nil {
		return nil
	}
	if current := c.current.Load(); current != nil && current.catalog.Version == version {
		return current
	}
	c.compileMu.Lock()
	defer c.compileMu.Unlock()
	if current := c.current.Load(); current != nil && current.catalog.Version == version {
		return current
	}
	return c.prepared[version]
}

// prepare validates and compiles one immutable snapshot per committed version.
// Callers must publish it only after their transaction commits, or discard it
// when the transaction rolls back.
func (c *Cache) prepare(catalog Catalog, wireLimit int) (*compiledCatalog, error) {
	if c == nil {
		return compileCatalogWithLimit(catalog, wireLimit)
	}
	c.compileMu.Lock()
	defer c.compileMu.Unlock()
	if current := c.current.Load(); current != nil {
		switch {
		case current.catalog.Version == catalog.Version:
			return current, nil
		case current.catalog.Version > catalog.Version:
			return nil, ErrConflict
		}
	}
	if prepared := c.prepared[catalog.Version]; prepared != nil && sameCatalogContent(catalog, prepared.catalog) {
		return prepared, nil
	}
	compiled, err := compileCatalogWithLimit(catalog, wireLimit)
	if err != nil {
		return nil, err
	}
	if c.prepared == nil {
		c.prepared = make(map[int64]*compiledCatalog)
	}
	c.prepared[catalog.Version] = compiled
	return compiled, nil
}

func (c *Cache) discardPrepared(compiled *compiledCatalog) {
	if c == nil || compiled == nil {
		return
	}
	c.compileMu.Lock()
	defer c.compileMu.Unlock()
	if c.prepared[compiled.catalog.Version] == compiled {
		delete(c.prepared, compiled.catalog.Version)
	}
}

// Replace atomically publishes a caller-independent catalog snapshot.
func (c *Cache) Replace(catalog Catalog) error {
	if c == nil {
		return nil
	}
	c.compileMu.Lock()
	defer c.compileMu.Unlock()
	current := c.current.Load()
	if current != nil && current.catalog.Version >= catalog.Version {
		return nil
	}
	compiled := c.prepared[catalog.Version]
	if compiled == nil {
		var err error
		compiled, err = compileCatalog(catalog)
		if err != nil {
			return err
		}
	}
	c.current.Store(compiled)
	c.removePreparedThrough(compiled.catalog.Version)
	return nil
}

func (c *Cache) publish(compiled *compiledCatalog) {
	if c == nil || compiled == nil {
		return
	}
	c.compileMu.Lock()
	defer c.compileMu.Unlock()
	current := c.current.Load()
	if current == nil || current.catalog.Version < compiled.catalog.Version {
		c.current.Store(compiled)
		c.removePreparedThrough(compiled.catalog.Version)
	}
}

func (c *Cache) removePreparedThrough(version int64) {
	for candidate := range c.prepared {
		if candidate <= version {
			delete(c.prepared, candidate)
		}
	}
}
