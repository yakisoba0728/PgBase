package schema

// CompiledCatalogForTest returns the immutable snapshot EnsureCurrent hands a
// pinned transaction. Only a snapshot compiled here carries the per-version
// artifacts, and the packages layered above schema cannot be imported from an
// in-package test, so their tests reach one through this.
func CompiledCatalogForTest(source Catalog) (Catalog, error) {
	compiled, err := compileCatalogWithLimit(source, maximumCatalogWireBytes)
	if err != nil {
		return Catalog{}, err
	}
	return compiled.catalog, nil
}
