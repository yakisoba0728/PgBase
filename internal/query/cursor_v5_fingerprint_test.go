package query

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/cursor"
)

func TestCursorV5QueryFingerprintUsesCanonicalTypedStableFieldPaths(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	compile := func(expression string) *plannedRelationFilter {
		t.Helper()
		plan, err := compileRelationListPlan(catalog, posts, mustParseFilter(t, expression), effectiveSortKeys(nil))
		if err != nil {
			t.Fatalf("compile filter %q: %v", expression, err)
		}
		return plan.filter
	}
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	first, err := cursorV5QueryFingerprint(codec, compile("'Ada' = owner.manager.name"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cursorV5QueryFingerprint(codec, compile("owner.manager.name='Ada'"))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := cursorV5QueryFingerprint(codec, compile("owner.manager.name='Zed'"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "a13637a1387add0def8dfa2279f130eba3e3ad5c7f045a8c9c3f645a0956cd7d"
	if hex.EncodeToString(first) != want {
		t.Fatalf("query fingerprint = %x, want %s", first, want)
	}
	if !bytes.Equal(first, second) || bytes.Equal(first, changed) {
		t.Fatalf("canonical/sensitive fingerprints = %x/%x/%x", first, second, changed)
	}

	// The digest is keyed: another cursor secret cannot reproduce it even for
	// the same canonical filter descriptor.
	rotated := cursor.NewCodec([]byte("abcdef0123456789abcdef0123456789"))
	rotatedHash, err := cursorV5QueryFingerprint(rotated, compile("owner.manager.name='Ada'"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, rotatedHash) || len(rotatedHash) != 32 {
		t.Fatalf("rotated query fingerprint = %x, original %x", rotatedHash, first)
	}
}

func TestCursorV5QueryFingerprintCanonicalizesCommutativeAssociativeGroups(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	compile := func(expression string) *plannedRelationFilter {
		t.Helper()
		plan, err := compileRelationListPlan(catalog, posts, mustParseFilter(t, expression), effectiveSortKeys(nil))
		if err != nil {
			t.Fatalf("compile filter %q: %v", expression, err)
		}
		return plan.filter
	}
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	first, err := cursorV5QueryFingerprint(codec, compile(
		"owner.name='Ada' && (owner.manager.name='Bob' && owner.manager.manager.name='Cid')",
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cursorV5QueryFingerprint(codec, compile(
		"(owner.manager.manager.name='Cid' && owner.name='Ada') && owner.manager.name='Bob'",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("equivalent filter group fingerprints differ: %x / %x", first, second)
	}
}
