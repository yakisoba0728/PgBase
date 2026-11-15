package cursor

import (
	"bytes"
	"crypto/sha256"
	"math"
	"testing"
)

func TestCursorEnvelopeSizeBoundary(t *testing.T) {
	t.Parallel()
	codec := NewCodec(bytes.Repeat([]byte{0x2a}, 32))
	payload := bytes.Repeat([]byte{'x'}, maxDecodedSize-sha256.Size)
	token, err := codec.signAndEncode(payload)
	if err != nil {
		t.Fatalf("maximum permitted payload: %v", err)
	}
	decoded, err := codec.authenticatedPayload(token)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("maximum permitted payload did not round-trip: %v", err)
	}
	if _, err := codec.signAndEncode(append(payload, 'x')); !IsInvalid(err) {
		t.Fatalf("oversized payload error = %v, want invalid cursor", err)
	}
}

func TestV5DescriptorMatchesCatalogVersionAtFullWidth(t *testing.T) {
	t.Parallel()
	query := bytes.Repeat([]byte{0x11}, sha256.Size)
	scope := bytes.Repeat([]byte{0x22}, sha256.Size)
	order := []V5OrderTerm{{Path: []string{"$id"}, Type: "uuid", Nulls: NullsLast}}
	for _, version := range []int64{7, 1 << 31, 1<<32 + 7, 1 << 62, math.MaxInt64} {
		descriptor := &V5Descriptor{CatalogVersion: version, QueryHash: query, ScopeHash: scope, Order: order}
		if !descriptor.Matches(version, query, scope, order) {
			t.Fatalf("exact catalog version %d did not match", version)
		}
		for _, different := range []int64{version ^ 1, version ^ (1 << 31), version ^ (1 << 32), version ^ (1 << 62)} {
			if descriptor.Matches(different, query, scope, order) {
				t.Fatalf("catalog version %d incorrectly matched %d", version, different)
			}
		}
	}
}
