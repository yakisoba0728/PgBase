//go:build integration

package doctor

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestRunDetectsARelationFunctionWhoseBodyIsOnlyComments installs a replacement
// that keeps every fragment a body-fragment matcher searched for and moves the
// executable statements into comments, so the trigger silently enforces
// nothing. Doctor compares the canonical definition instead of looking for
// fragments, so the substitution is drift.
func TestRunDetectsARelationFunctionWhoseBodyIsOnlyComments(t *testing.T) {
	for _, testCase := range []struct {
		name string
		pick func(relationDoctorFixture) string
		code string
	}{
		{
			name: "position function",
			pick: func(fixture relationDoctorFixture) string { return fixture.multiNames.PositionFunction },
			code: "relation_multi_position_function_mismatch",
		},
		{
			name: "cardinality function",
			pick: func(fixture relationDoctorFixture) string { return fixture.multiNames.CardinalityFunction },
			code: "relation_multi_cardinality_function_mismatch",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newRelationDoctorFixture(t)
			name := pgx.Identifier{"pgbase_relation", testCase.pick(fixture)}.Sanitize()
			edge := "pgbase_relation." + fixture.multiNames.MultiTable
			source := "app." + "posts"
			spoof := `create or replace function ` + name + `() returns trigger
				language plpgsql as $spoof$
			DECLARE
				affected_sources uuid[];
				relation_count bigint;
				minimum_position bigint;
				maximum_position bigint;
			BEGIN
				-- IF TG_OP = 'INSERT' THEN
				--   affected_sources := ARRAY[NEW.source_id];
				-- ELSIF TG_OP = 'DELETE' THEN
				--   affected_sources := ARRAY[OLD.source_id];
				-- ELSE
				--   affected_sources := ARRAY[OLD.source_id, NEW.source_id];
				-- END IF;
				-- IF TG_TABLE_SCHEMA = 'app' AND TG_TABLE_NAME = 'posts' THEN
				--   SELECT COUNT(*), MIN(position), MAX(position)
				--   INTO relation_count, minimum_position, maximum_position
				--   FROM ` + edge + ` WHERE source_id = affected_source;
				--   IF EXISTS (SELECT 1 FROM ` + source + ` WHERE id = affected_source) THEN
				--     IF minimum_position <> 0 OR maximum_position::bigint <> relation_count - 1 THEN
				--       RAISE EXCEPTION 'drift' USING ERRCODE = '23514',
				--         CONSTRAINT = '` + fixture.multiNames.MultiPositionCheck + `';
				--     END IF;
				--   END IF;
				-- END IF;
				RETURN NULL;
			END;
			$spoof$`
			if _, err := fixture.pool.Exec(context.Background(), spoof); err != nil {
				t.Fatalf("install spoofed relation function: %v", err)
			}
			report := runDoctor(t, fixture.pool)
			assertFindingCode(t, report, testCase.code)
			assertRelationFindingsRedacted(t, report, fixture)
		})
	}
}
