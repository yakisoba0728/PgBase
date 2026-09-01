package cursor

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestV5DescriptorMatchesEverySignedQueryBinding(t *testing.T) {
	query := bytes.Repeat([]byte{0x11}, sha256.Size)
	scope := bytes.Repeat([]byte{0x22}, sha256.Size)
	order := []V5OrderTerm{
		{Path: []string{"$created_at"}, Type: "timestamptz", Nulls: NullsLast},
		{Path: []string{"$id"}, Type: "uuid", Nulls: NullsLast},
	}
	descriptor := &V5Descriptor{CatalogVersion: 7, QueryHash: query, ScopeHash: scope, Order: order}
	if !descriptor.Matches(7, query, scope, order) {
		t.Fatal("exact v5 descriptor binding did not match")
	}

	tests := []struct {
		name    string
		catalog int64
		query   []byte
		scope   []byte
		order   []V5OrderTerm
	}{
		{name: "catalog", catalog: 8, query: query, scope: scope, order: order},
		{name: "query", catalog: 7, query: bytes.Repeat([]byte{0x33}, sha256.Size), scope: scope, order: order},
		{name: "scope", catalog: 7, query: query, scope: bytes.Repeat([]byte{0x44}, sha256.Size), order: order},
		{name: "order", catalog: 7, query: query, scope: scope, order: []V5OrderTerm{
			{Path: []string{"$created_at"}, Type: "timestamptz", Desc: true, Nulls: NullsLast},
			{Path: []string{"$id"}, Type: "uuid", Nulls: NullsLast},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if descriptor.Matches(test.catalog, test.query, test.scope, test.order) {
				t.Fatal("mismatched v5 descriptor binding matched")
			}
		})
	}
	if (*V5Descriptor)(nil).Matches(7, query, scope, order) {
		t.Fatal("nil v5 descriptor matched")
	}
}
