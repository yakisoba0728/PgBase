package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// MembershipExecer is the database handle subset the rollout helpers need, so
// a caller can pass either a pool or a bare connection.
type MembershipExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Ordinals are only filled in where they are missing, and numbered on from the
// counter the collection already reached, so the statement is safe to run again
// after a test seeds more rows.
const reserveMissingCreationOrdinals = `do $backfill$
declare
	collection record;
	assigned bigint;
	reserved bigint;
	upper_created_at timestamptz;
	upper_id uuid;
	stamp timestamptz := transaction_timestamp();
begin
	for collection in
		select id, table_name from pgbase_meta.collections order by id
	loop
		select coalesce(max(ordinal), 0) into reserved
		from pgbase_meta.record_creation_ordinals
		where collection_id = collection.id;
		execute format(
			'insert into pgbase_meta.record_creation_ordinals (collection_id, record_id, ordinal)
			select $1, unassigned.id,
				$2 + row_number() over (order by unassigned.created_at, unassigned.id)
			from app.%I as unassigned
			where not exists (
				select 1 from pgbase_meta.record_creation_ordinals as assigned
				where assigned.collection_id = $1 and assigned.record_id = unassigned.id
			)', collection.table_name)
			using collection.id, reserved;
		select count(*), coalesce(max(ordinal), 0) into assigned, reserved
		from pgbase_meta.record_creation_ordinals
		where collection_id = collection.id;
		execute format(
			'select created_at, id from app.%I order by created_at desc, id desc limit 1',
			collection.table_name)
			into upper_created_at, upper_id;
		update pgbase_meta.collection_creation_counters set
			generation = 1, last_ordinal = reserved, updated_at = stamp
			where collection_id = collection.id;
		update pgbase_meta.record_membership_backfills set
			generation = 1,
			state = 'ready',
			cutover_count = assigned,
			cutover_last_created_at = upper_created_at,
			cutover_last_record_id = upper_id,
			checkpoint_created_at = null,
			checkpoint_record_id = null,
			last_assigned_ordinal = assigned,
			validated_base_count = assigned,
			validated_sidecar_count = assigned,
			updated_at = stamp,
			completed_at = stamp
			where collection_id = collection.id;
	end loop;
end
$backfill$`

// The timeline is backdated rather than slept through: the v7 CHECK only
// requires the stamps to be ordered, and the drained cursor TTL is a
// precondition of retirement rather than something a test can wait out.
const retireMembershipCapability = `update pgbase_meta.record_membership_capability set
	state = 'ready',
	generation = 1,
	minimum_writer_version = 2,
	cursor_issue_version = 5,
	cutover_at = transaction_timestamp() - interval '20 minutes',
	cutover_collection_count = (select count(*) from pgbase_meta.collections),
	cutover_record_count = (select count(*) from pgbase_meta.record_creation_ordinals),
	ready_at = transaction_timestamp() - interval '19 minutes',
	ready_collection_count = (select count(*) from pgbase_meta.collections),
	v5_enabled_at = transaction_timestamp() - interval '16 minutes',
	v4_decode_until = transaction_timestamp() - interval '1 minute',
	v4_retired_at = transaction_timestamp(),
	updated_at = transaction_timestamp()
	where singleton`

// RetireRecordMembershipV4 reserves a sidecar ordinal for every catalogued
// record and then moves the capability to the retired terminal state Release C
// requires. Tests that exercise an earlier release drive the capability
// themselves and must not call this.
func RetireRecordMembershipV4(t testing.TB, ctx context.Context, database MembershipExecer) {
	t.Helper()
	if _, err := database.Exec(ctx, reserveMissingCreationOrdinals); err != nil {
		t.Fatalf("reserve record creation ordinals: %v", err)
	}
	RetireRecordMembershipCapability(t, ctx, database)
}

// RetireRecordMembershipCapability moves only the capability row, for fixtures
// whose catalog rows do not have real collection tables behind them.
func RetireRecordMembershipCapability(t testing.TB, ctx context.Context, database MembershipExecer) {
	t.Helper()
	if _, err := database.Exec(ctx, retireMembershipCapability); err != nil {
		t.Fatalf("retire record membership cursor v4: %v", err)
	}
}
