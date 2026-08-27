package schema

import "sync/atomic"

// Cache publishes immutable catalog snapshots atomically.
type Cache struct {
	current atomic.Pointer[Catalog]
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
	return cloneCatalog(*current)
}

// Replace atomically publishes a caller-independent catalog snapshot.
func (c *Cache) Replace(catalog Catalog) {
	if c == nil {
		return
	}
	immutable := cloneCatalog(catalog)
	for {
		current := c.current.Load()
		if current != nil && current.Version > immutable.Version {
			return
		}
		if c.current.CompareAndSwap(current, &immutable) {
			return
		}
	}
}
