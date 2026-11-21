package query

import (
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// A caller-owned catalog has no shared computation slot, so nothing derived
// from it may outlive the call: editing the catalog between calls must be
// observed, exactly as the former per-call scan and digest observed it.
func TestCatalogDerivedIsPrivateToUncompiledCatalogs(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	catalog := schema.Catalog{Collections: map[string]schema.Collection{
		"posts": {ID: id, Name: "posts", TableName: "posts", Fields: []schema.Field{
			{ID: uuid.MustParse("66666666-6666-4666-8666-666666666667"), Name: "title", Type: schema.TypeText},
		}},
	}}
	if derivedCatalog(catalog) == derivedCatalog(catalog) {
		t.Fatal("an uncompiled catalog shared derived metadata across calls")
	}
	if found, ok := catalogCollectionByID(catalog, id); !ok || found.Name != "posts" {
		t.Fatalf("lookup = %#v, %t", found, ok)
	}
	first, err := derivedCatalog(catalog).schemaDigest(catalog.Collections["posts"])
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := recordartifact.SchemaDigest(catalog.Collections["posts"]); first != want {
		t.Fatalf("digest = %x, want %x", first, want)
	}

	changed := catalog.Collections["posts"]
	changed.Fields = append(changed.Fields, schema.Field{ID: uuid.MustParse("66666666-6666-4666-8666-666666666668"), Name: "body", Type: schema.TypeText, Ordinal: 1})
	catalog.Collections["posts"] = changed
	second, err := derivedCatalog(catalog).schemaDigest(catalog.Collections["posts"])
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("digest of an edited caller-owned catalog was served from a stale cache")
	}
	delete(catalog.Collections, "posts")
	if _, ok := catalogCollectionByID(catalog, id); ok {
		t.Fatal("lookup of an edited caller-owned catalog was served from a stale cache")
	}
}

func TestCatalogCollectionByIDMatchesUniqueNameKeyedScan(t *testing.T) {
	t.Parallel()
	a := uuid.MustParse("77777777-7777-4777-8777-777777777771")
	b := uuid.MustParse("77777777-7777-4777-8777-777777777772")
	c := uuid.MustParse("77777777-7777-4777-8777-777777777773")
	catalog := schema.Catalog{Collections: map[string]schema.Collection{
		"a":       {ID: a, Name: "a"},
		"b1":      {ID: b, Name: "b1"},
		"b2":      {ID: b, Name: "b2"},
		"forged":  {ID: c, Name: "other"},
		"c":       {ID: c, Name: "c"},
		"nil":     {ID: uuid.Nil, Name: "nil"},
		"missing": {ID: uuid.MustParse("77777777-7777-4777-8777-777777777774"), Name: "elsewhere"},
	}}
	for _, test := range []struct {
		id   uuid.UUID
		name string
		ok   bool
	}{
		{id: a, name: "a", ok: true},
		{id: b},
		{id: c, name: "c", ok: true},
		{id: uuid.Nil},
		{id: uuid.MustParse("77777777-7777-4777-8777-777777777774")},
		{id: uuid.MustParse("77777777-7777-4777-8777-777777777775")},
	} {
		found, ok := catalogCollectionByID(catalog, test.id)
		if ok != test.ok || found.Name != test.name {
			t.Fatalf("catalogCollectionByID(%s) = %q, %t; want %q, %t", test.id, found.Name, ok, test.name, test.ok)
		}
	}
}
