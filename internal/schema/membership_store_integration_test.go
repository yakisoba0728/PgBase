//go:build integration

package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestCollectionLifecycleMaintainsMembershipMetadataOutsideThePublicPlan(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()

	offPlan, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{Name: "membership_off"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range offPlan.Statements {
		if strings.Contains(statement, "pgbase_meta.record_membership") ||
			strings.Contains(statement, "pgbase_meta.collection_creation") ||
			strings.Contains(statement, "pgbase_meta.record_creation") {
			t.Fatalf("public schema plan leaked membership maintenance: %q", statement)
		}
	}
	off := applyFreshPlan(t, service, CreateCollectionInput{Name: "membership_off"}, administratorID)
	assertMembershipCollectionState(t, pool, off.ID, 0, "off", false)

	if _, err := pool.Exec(ctx, `update pgbase_meta.record_membership_capability set
		state = 'ready', generation = 1, minimum_writer_version = $1,
		cursor_issue_version = 4, cutover_at = current_timestamp,
		cutover_collection_count = 1, cutover_record_count = 0,
		ready_at = current_timestamp, ready_collection_count = 1,
		updated_at = current_timestamp
		where singleton`, int16(1)); err != nil {
		t.Fatal(err)
	}
	ready := applyFreshPlan(t, service, CreateCollectionInput{Name: "membership_ready"}, administratorID)
	assertMembershipCollectionState(t, pool, ready.ID, 1, "ready", true)

	plan, err := service.Preview(ctx, NewDeleteChange(ready.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDelete(ctx, service, ready.Name, plan, administratorID); err != nil {
		t.Fatal(err)
	}
	var counters, backfills int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from pgbase_meta.collection_creation_counters where collection_id = $1),
		(select count(*) from pgbase_meta.record_membership_backfills where collection_id = $1)`,
		ready.ID).Scan(&counters, &backfills); err != nil {
		t.Fatal(err)
	}
	if counters != 0 || backfills != 0 {
		t.Fatalf("deleted collection membership rows = %d/%d, want 0/0", counters, backfills)
	}
}

func assertMembershipCollectionState(
	t *testing.T,
	pool *database.Pool,
	collectionID uuid.UUID,
	wantGeneration int64,
	wantState string,
	wantCompleted bool,
) {
	t.Helper()
	var counterGeneration, lastOrdinal, backfillGeneration int64
	var cutoverCount sql.NullInt64
	var state string
	var completed bool
	if err := pool.QueryRow(context.Background(), `select
		counter.generation, counter.last_ordinal,
		backfill.generation, backfill.state, backfill.cutover_count,
		backfill.completed_at is not null
		from pgbase_meta.collection_creation_counters as counter
		join pgbase_meta.record_membership_backfills as backfill using (collection_id)
		where counter.collection_id = $1`, collectionID).Scan(
		&counterGeneration, &lastOrdinal, &backfillGeneration, &state, &cutoverCount, &completed,
	); err != nil {
		t.Fatal(err)
	}
	if counterGeneration != wantGeneration || backfillGeneration != wantGeneration ||
		lastOrdinal != 0 || state != wantState || cutoverCount.Valid != wantCompleted ||
		(cutoverCount.Valid && cutoverCount.Int64 != 0) || completed != wantCompleted {
		t.Fatalf("membership collection state = counter %d/%d backfill %d/%s/%v/%t",
			counterGeneration, lastOrdinal, backfillGeneration, state, cutoverCount, completed)
	}
}
