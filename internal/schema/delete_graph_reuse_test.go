// Package schema_test owns the delete-graph reuse gate. The graph belongs to
// internal/relation, but only internal/schema can produce the compiled catalog
// that carries it, and an in-package test cannot import relation without a
// cycle. So the gate lives here, outside both packages.
package schema_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// deleteGraphAllocationCeiling is what one delete may spend on obtaining the
// graph. Reuse costs a slot read; rebinding costs hundreds of allocations per
// collection, so anything above a handful means the per-version compilation
// was lost.
const deleteGraphAllocationCeiling = 4

// deleteGraphCatalog builds a relation-free catalog of the requested size. The
// audit measured this shape: an empty collection is the cheapest possible
// input, so whatever cost remains is the rebuild itself.
func deleteGraphCatalog(count int) schema.Catalog {
	catalog := schema.Catalog{Version: 1, Collections: make(map[string]schema.Collection, count)}
	for index := range count {
		name := fmt.Sprintf("bench_%04d", index)
		catalog.Collections[name] = schema.Collection{
			ID:        uuid.NewSHA1(uuid.NameSpaceOID, []byte("delete-graph:"+name)),
			Name:      name,
			TableName: name,
		}
	}
	return catalog
}

func compiledDeleteGraphCatalog(t *testing.T, count int) schema.Catalog {
	t.Helper()
	compiled, err := schema.CompiledCatalogForTest(deleteGraphCatalog(count))
	if err != nil {
		t.Fatalf("compile catalog of %d collections: %v", count, err)
	}
	return compiled
}

// TestDeleteGraphCostDoesNotGrowWithTheCatalog is the regression gate. Binding
// walks every collection, so a per-request bind makes each delete attempt cost
// the whole schema; the SQL statement-count gates cannot see that because no
// statement is involved. Allocations are the stable signal here, and a rebuild
// cannot hide inside this bound at 500 collections.
func TestDeleteGraphCostDoesNotGrowWithTheCatalog(t *testing.T) {
	small := compiledDeleteGraphCatalog(t, 10)
	large := compiledDeleteGraphCatalog(t, 500)

	smallAllocations := testing.AllocsPerRun(100, func() {
		if _, err := relation.BindDeleteGraph(small); err != nil {
			t.Fatalf("bind small delete graph: %v", err)
		}
	})
	largeAllocations := testing.AllocsPerRun(100, func() {
		if _, err := relation.BindDeleteGraph(large); err != nil {
			t.Fatalf("bind large delete graph: %v", err)
		}
	})

	if largeAllocations > deleteGraphAllocationCeiling || smallAllocations > deleteGraphAllocationCeiling {
		t.Fatalf("delete graph allocations per bind: 10 collections = %.0f, 500 collections = %.0f, ceiling %d",
			smallAllocations, largeAllocations, deleteGraphAllocationCeiling)
	}
	if largeAllocations > smallAllocations {
		t.Fatalf("delete graph allocations grow with the catalog: 10 collections = %.0f, 500 collections = %.0f",
			smallAllocations, largeAllocations)
	}
}

// TestDeleteGraphIsBoundOncePerCatalogVersion pins the sharing itself: readers
// of one pinned version must observe the same graph, whatever order they
// arrive in. Concurrent binds are the normal case, since every delete on a
// version races every other one.
func TestDeleteGraphIsBoundOncePerCatalogVersion(t *testing.T) {
	catalog := compiledDeleteGraphCatalog(t, 8)
	const readers = 16

	graphs := make([]*relation.DeleteGraph, readers)
	var waiting, running sync.WaitGroup
	waiting.Add(readers)
	running.Add(1)
	for index := range readers {
		go func() {
			defer waiting.Done()
			running.Wait()
			graph, err := relation.BindDeleteGraph(catalog)
			if err != nil {
				t.Errorf("bind delete graph: %v", err)
				return
			}
			graphs[index] = graph
		}()
	}
	running.Done()
	waiting.Wait()

	for index, graph := range graphs {
		if graph == nil || graph != graphs[0] {
			t.Fatalf("reader %d observed a different delete graph: %p vs %p", index, graph, graphs[0])
		}
	}
}

// TestDeleteGraphIsPrivateToAnUncompiledCatalog keeps the reuse tied to the
// snapshot the cache published. A caller-owned copy can be mutated, so it must
// never be answered from another catalog's graph.
func TestDeleteGraphIsPrivateToAnUncompiledCatalog(t *testing.T) {
	catalog := deleteGraphCatalog(4)
	first, err := relation.BindDeleteGraph(catalog)
	if err != nil {
		t.Fatalf("bind uncompiled delete graph: %v", err)
	}
	second, err := relation.BindDeleteGraph(catalog)
	if err != nil {
		t.Fatalf("rebind uncompiled delete graph: %v", err)
	}
	if first == nil || first == second {
		t.Fatal("an uncompiled catalog shared a delete graph across binds")
	}
}

func BenchmarkBindDeleteGraph(b *testing.B) {
	for _, count := range []int{10, 100, 500} {
		uncompiled := deleteGraphCatalog(count)
		compiled, err := schema.CompiledCatalogForTest(uncompiled)
		if err != nil {
			b.Fatal(err)
		}
		for name, catalog := range map[string]schema.Catalog{"uncompiled": uncompiled, "compiled": compiled} {
			b.Run(fmt.Sprintf("%s/%d", name, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := relation.BindDeleteGraph(catalog); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
