package query

import (
	"strings"
	"testing"
)

func TestExpansionPolicyPredicatePrecedesOrderingAndLimit(t *testing.T) {
	base := `SELECT source_records.id FROM app.children AS source_records WHERE source_records.parent_id = any($1::uuid[]) ORDER BY source_records.parent_id LIMIT $3`
	got := injectBeforeOrder(base, ` AND (source_records.owner_id = $2)`)
	policy := strings.Index(got, "owner_id")
	order := strings.Index(got, " ORDER BY ")
	limit := strings.Index(got, " LIMIT ")
	if policy < 0 || policy > order || order > limit {
		t.Fatalf("predicate was not injected before ordering and limit: %s", got)
	}
}
