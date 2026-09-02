//go:build integration

package doctor

import (
	"context"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	companionCollection = "sortable"
	companionField      = "nickname"
)

// newCompanionPool builds the smallest catalog that owns exactly one companion
// index: a nullable indexed text field, whose descending page is the ordering no
// default btree can serve.
func newCompanionPool(t *testing.T) *database.Pool {
	t.Helper()
	pool := newDoctorPool(t)
	collectionID := insertCollection(t, pool, companionCollection, companionCollection)
	insertField(t, pool, collectionID, companionField, "text", false, false, true, 0)
	if _, err := pool.Exec(context.Background(), `create table app.sortable (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		nickname text
	);
	create index sortable_nickname_idx on app.sortable (nickname);
	create index sortable_page_idx on app.sortable (created_at, id)`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func companionIndexName() string {
	return schema.NullableDescCompanionIndexName(companionCollection, companionField)
}

func createCompanionIndex(t *testing.T, pool *database.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `create index `+companionIndexName()+
		` on app.sortable (nickname desc nulls last, id desc nulls first)`); err != nil {
		t.Fatal(err)
	}
}

func setCompanionState(t *testing.T, pool *database.Pool, state string, indexCount int) {
	t.Helper()
	// Migration 9 requires a non-pending row to name the generation its rollout was
	// proved at. Doctor reads that state rather than producing it, so the seed stamps
	// the current generation instead of the production check being weakened for it.
	statement := `update pgbase_meta.schema_companion_index_capability set
		state = 'building_v3', generation = 1, proven_compiler_version = 3,
		catalog_version = (select version from pgbase_meta.schema_state where singleton),
		building_at = transaction_timestamp(), ready_at = null, ready_index_count = null,
		updated_at = transaction_timestamp() where singleton`
	if state == "ready" {
		statement = `update pgbase_meta.schema_companion_index_capability set
			state = 'ready_v3', generation = 1, proven_compiler_version = 3,
			catalog_version = (select version from pgbase_meta.schema_state where singleton),
			building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
			ready_index_count = $1, updated_at = transaction_timestamp() where singleton`
		if _, err := pool.Exec(context.Background(), statement, indexCount); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

// While the capability is pending nothing has promised a companion, so its
// absence is not a finding and its presence is not an orphan. The second half is
// the rollback case: a binary that predates the rollout still reads a clean
// report on a database where the indexes were already built.
func TestDoctorTreatsCompanionIndexesAsOptionalWhilePending(t *testing.T) {
	pool := newCompanionPool(t)
	if report := runDoctor(t, pool); !report.OK {
		t.Fatalf("pending report without the companion = %#v", report)
	}
	createCompanionIndex(t, pool)
	if report := runDoctor(t, pool); !report.OK {
		t.Fatalf("pending report with an extra companion = %#v", report)
	}
}

func TestDoctorReportsAnUnfinishedCompanionIndexRollout(t *testing.T) {
	pool := newCompanionPool(t)
	setCompanionState(t, pool, "building", 0)
	assertFindingCode(t, runDoctor(t, pool), "schema_companion_index_build_incomplete")

	// Building stays incomplete even once the objects exist: only the promotion
	// that revalidates the catalog may close the rollout.
	createCompanionIndex(t, pool)
	assertFindingCode(t, runDoctor(t, pool), "schema_companion_index_build_incomplete")
}

func TestDoctorRequiresCompanionIndexesOnlyWhenReady(t *testing.T) {
	pool := newCompanionPool(t)
	createCompanionIndex(t, pool)
	setCompanionState(t, pool, "ready", 1)
	if report := runDoctor(t, pool); !report.OK {
		t.Fatalf("ready report with a complete rollout = %#v", report)
	}

	if _, err := pool.Exec(context.Background(), "drop index app."+companionIndexName()); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "schema_companion_index_missing")
}

func TestDoctorReportsACompanionIndexWithTheWrongKeyOrder(t *testing.T) {
	pool := newCompanionPool(t)
	// PostgreSQL's plain DESC default is NULLS FIRST, which is precisely the
	// ordering the public contract does not use, so it must not satisfy it.
	if _, err := pool.Exec(context.Background(), `create index `+companionIndexName()+
		` on app.sortable (nickname desc, id desc)`); err != nil {
		t.Fatal(err)
	}
	setCompanionState(t, pool, "ready", 1)
	assertFindingCode(t, runDoctor(t, pool), "schema_companion_index_missing")
}

// An interrupted concurrent build is a distinct, recoverable condition in every
// state. The flag is set directly here because this asserts how doctor reports
// the residue; internal/schema proves that a killed build actually produces it.
func TestDoctorReportsAnInvalidCompanionIndexAsRecoverable(t *testing.T) {
	for _, state := range []string{"pending", "building", "ready"} {
		t.Run(state, func(t *testing.T) {
			pool := newCompanionPool(t)
			createCompanionIndex(t, pool)
			if _, err := pool.Exec(context.Background(),
				`update pg_index set indisvalid = false where indexrelid = $1::regclass`,
				"app."+companionIndexName()); err != nil {
				t.Fatal(err)
			}
			if state != "pending" {
				setCompanionState(t, pool, state, 1)
			}
			report := runDoctor(t, pool)
			assertFindingCode(t, report, "schema_companion_index_invalid")
			for _, found := range report.Findings {
				if found.Code == "schema_companion_index_missing" {
					t.Fatalf("recoverable residue reported as a broken contract: %#v", report.Findings)
				}
			}
		})
	}
}
