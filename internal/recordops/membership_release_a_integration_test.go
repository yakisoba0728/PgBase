//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/tests/testsupport"
)

const releaseADelayedCreateLockKey int64 = 5_922_338_308_289_414_402

func TestReleaseACreateAcquiresOffStateFenceForEveryWireMode(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, test := range []struct {
		name           string
		idempotencyKey string
		replay         bool
	}{
		{name: "headerless"},
		{name: "guarded", idempotencyKey: "release-a-fence-guarded-0001"},
		{name: "guarded_replay", idempotencyKey: "release-a-fence-replay-0001", replay: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := CreateRequest{
				Principal: principal, RequestID: "release-a-fence-" + test.name,
				Collection: "records", IdempotencyKey: test.idempotencyKey,
				Input: map[string]json.RawMessage{
					"title": json.RawMessage(`"release-a-fence-` + test.name + `"`),
				},
			}
			if test.replay {
				seeded, err := records.Create(ctx, request)
				if err != nil || seeded.Replayed {
					t.Fatalf("seed guarded replay = %#v/%v", seeded, err)
				}
			}
			connection, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			blocker, err := connection.BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err := blocker.Exec(ctx, `select counters.last_ordinal
				from pgbase_meta.collection_creation_counters as counters
				join pgbase_meta.collections as collection on collection.id = counters.collection_id
				where collection.table_name = 'records' for update of counters`); err != nil {
				t.Fatal(err)
			}

			result := make(chan struct {
				mutation MutationResult
				err      error
			}, 1)
			go func() {
				mutation, createErr := records.Create(ctx, request)
				result <- struct {
					mutation MutationResult
					err      error
				}{mutation: mutation, err: createErr}
			}()

			select {
			case completed := <-result:
				t.Fatalf("Create completed without the off-state creation fence: %#v/%v",
					completed.mutation, completed.err)
			case <-time.After(150 * time.Millisecond):
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case completed := <-result:
				if completed.err != nil || completed.mutation.Replayed != test.replay {
					t.Fatalf("Create after fence release = %#v/%v, want replay=%t",
						completed.mutation, completed.err, test.replay)
				}
			case <-ctx.Done():
				t.Fatalf("Create did not finish after fence release: %v", ctx.Err())
			}
		})
	}
}

func TestReleaseARejectsInjectedA0WriterCapability(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA0)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	_, err = records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "release-a-reject-a0", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"must-not-bypass"`)},
	})
	if !IsCode(err, CodeUnavailable) || errors.Is(err, membership.ErrWriterTooOld) {
		t.Fatalf("Create with injected A0 runtime error = %v, want stable availability", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from app.records`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("injected A0 runtime wrote %d base rows", count)
	}
}

func TestReleaseADirectConstructorRetriesRegistrationAfterMigrationM(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Rewind to exactly v6. Both later migrations have to go: leaving v8's row
	// behind would make the history a gap rather than the clean pre-membership
	// history this test is about.
	if _, err := pool.Exec(ctx, `drop table pgbase_meta.schema_writer_leases;
		drop table pgbase_meta.schema_companion_index_capability;
		drop table pgbase_meta.record_creation_ordinals;
		drop table pgbase_meta.record_membership_backfills;
		drop table pgbase_meta.collection_creation_counters;
		drop table pgbase_meta.runtime_writer_leases;
		drop table pgbase_meta.record_membership_capability;
		delete from pgbase_meta.system_migrations where version in (7, 8)`); err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Principal: principal, RequestID: "release-a-migration-retry", Collection: "records",
		IdempotencyKey: "release-a-migration-retry-0001",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"after-migration-m"`)},
	}
	if _, err := records.Create(ctx, request); !IsCode(err, CodeUnavailable) || errors.Is(err, membership.ErrUnavailable) {
		t.Fatalf("Release-A Create on exact v6 error = %v, want stable availability", err)
	}
	// Apply every pending migration rather than targeting 7: the exact-target
	// command refuses a version that leaves a later embedded migration pending,
	// which is the guard that keeps a rollout from crossing two at once. What
	// this test needs is simply that membership exists afterwards.
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := records.Create(ctx, request)
	if err != nil {
		t.Fatalf("same direct service after migration M: %v", err)
	}
	if created.Record == nil || created.Replayed {
		t.Fatalf("post-migration Create = %#v", created)
	}
}

func TestReleaseADirectConstructorConcurrentFirstUseFitsTwoConnectionPool(t *testing.T) {
	databaseURL, _, _, _, principal := newRecordOperationsTestWithURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parameters := parsed.Query()
	parameters.Set("pool_max_conns", "2")
	parsed.RawQuery = parameters.Encode()
	limitedPool, err := database.Open(ctx, parsed.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(limitedPool.Close)
	schemas := schema.NewService(limitedPool, &schema.Cache{}, schema.Planner{})
	records := NewService(limitedPool, schemas, []byte("0123456789abcdef0123456789abcdef"))

	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			_, createErr := records.Create(ctx, CreateRequest{
				Principal: principal, RequestID: fmt.Sprintf("release-a-small-pool-%d", index), Collection: "records",
				Input: map[string]json.RawMessage{
					"title": json.RawMessage(fmt.Sprintf(`"small-pool-%d"`, index)),
				},
			})
			results <- createErr
		}(index)
	}
	close(start)
	for index := 0; index < 2; index++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent first-use Create %d: %v", index, err)
			}
		case <-ctx.Done():
			t.Fatalf("concurrent first-use Create deadlocked with two connections: %v", ctx.Err())
		}
	}
}

func TestReleaseAUpdateAndDeleteBeginWithWriterFence(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "release-a-fenced-mutations-create", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"before"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	recordID := recordResultID(t, created)

	blockCapability := func() (pgx.Tx, func()) {
		t.Helper()
		connection, acquireErr := pool.Acquire(ctx)
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		tx, beginErr := connection.Begin(ctx)
		if beginErr != nil {
			connection.Release()
			t.Fatal(beginErr)
		}
		if _, lockErr := tx.Exec(ctx, `select singleton
			from pgbase_meta.record_membership_capability where singleton for update`); lockErr != nil {
			_ = tx.Rollback(ctx)
			connection.Release()
			t.Fatal(lockErr)
		}
		return tx, connection.Release
	}

	updateBlocker, releaseUpdateConnection := blockCapability()
	updateResult := make(chan struct {
		result MutationResult
		err    error
	}, 1)
	go func() {
		result, updateErr := records.Update(ctx, UpdateRequest{
			Principal: principal, RequestID: "release-a-fenced-update", Collection: "records", ID: recordID,
			IfMatch: EntityTagSet{Present: true, Values: []string{created.Record.ETag}},
			Input:   map[string]json.RawMessage{"title": json.RawMessage(`"after"`)},
		})
		updateResult <- struct {
			result MutationResult
			err    error
		}{result: result, err: updateErr}
	}()
	select {
	case result := <-updateResult:
		t.Fatalf("Update bypassed the writer fence: %#v", result)
	case <-time.After(150 * time.Millisecond):
	}
	if err := updateBlocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	releaseUpdateConnection()
	updated := <-updateResult
	if updated.err != nil || updated.result.Record == nil {
		t.Fatalf("Update after writer fence release = %#v/%v", updated.result, updated.err)
	}

	deleteBlocker, releaseDeleteConnection := blockCapability()
	deleteResult := make(chan error, 1)
	go func() {
		_, deleteErr := records.Delete(ctx, DeleteRequest{
			Principal: principal, RequestID: "release-a-fenced-delete", Collection: "records", ID: recordID,
			IfMatch: EntityTagSet{Present: true, Values: []string{updated.result.Record.ETag}},
		})
		deleteResult <- deleteErr
	}()
	select {
	case err := <-deleteResult:
		t.Fatalf("Delete bypassed the writer fence: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := deleteBlocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	releaseDeleteConnection()
	if err := <-deleteResult; err != nil {
		t.Fatalf("Delete after writer fence release: %v", err)
	}
}

func TestReleaseACreateReservationAndSidecarRollBackTogether(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	controller := membership.New(pool)
	if _, err := controller.Cutover(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtime.FleetMember()},
	}); err != nil {
		t.Fatal(err)
	}

	var collectionID uuid.UUID
	if err := pool.QueryRow(ctx, `select id from pgbase_meta.collections
		where table_name = 'records'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_release_a_create_outbox()
		returns trigger language plpgsql as $$ begin
			raise exception 'injected release-a create failure';
		end $$;
		create trigger fail_release_a_create_outbox before insert on pgbase_meta.record_outbox
		for each row when (new.operation = 'create')
		execute function pgbase_meta.fail_release_a_create_outbox()`); err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Principal: principal, RequestID: "release-a-reserve-rollback", Collection: "records",
		IdempotencyKey: "release-a-reserve-rollback-0001",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"reserve-rollback"`)},
	}
	if _, err := records.Create(ctx, request); err == nil {
		t.Fatal("injected create failure succeeded")
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 0, 0, 0)

	if _, err := pool.Exec(ctx, `drop trigger fail_release_a_create_outbox on pgbase_meta.record_outbox;
		drop function pgbase_meta.fail_release_a_create_outbox()`); err != nil {
		t.Fatal(err)
	}
	created, err := records.Create(ctx, request)
	if err != nil {
		t.Fatalf("retry after rolled-back reservation: %v", err)
	}
	if created.Replayed {
		t.Fatal("rolled-back create was incorrectly replayed")
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 1, 1, 1)
	var ordinal int64
	if err := pool.QueryRow(ctx, `select ordinal from pgbase_meta.record_creation_ordinals
		where collection_id = $1 and record_id = $2`, collectionID, recordResultID(t, created)).Scan(&ordinal); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("retry ordinal = %d, want 1", ordinal)
	}
}

func TestReleaseADeleteAndSidecarRemovalRollBackTogether(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	if _, err := membership.New(pool).Cutover(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtime.FleetMember()},
	}); err != nil {
		t.Fatal(err)
	}
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "release-a-delete-create", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"delete-atomic"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var collectionID uuid.UUID
	if err := pool.QueryRow(ctx, `select id from pgbase_meta.collections
		where table_name = 'records'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 1, 1, 1)

	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_release_a_delete_outbox()
		returns trigger language plpgsql as $$ begin
			raise exception 'injected release-a delete failure';
		end $$;
		create trigger fail_release_a_delete_outbox before insert on pgbase_meta.record_outbox
		for each row when (new.operation = 'delete')
		execute function pgbase_meta.fail_release_a_delete_outbox()`); err != nil {
		t.Fatal(err)
	}
	request := DeleteRequest{
		Principal: principal, RequestID: "release-a-delete-rollback", Collection: "records",
		ID:      recordResultID(t, created),
		IfMatch: EntityTagSet{Present: true, Values: []string{created.Record.ETag}},
	}
	if _, err := records.Delete(ctx, request); err == nil {
		t.Fatal("injected delete failure succeeded")
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 1, 1, 1)

	if _, err := pool.Exec(ctx, `drop trigger fail_release_a_delete_outbox on pgbase_meta.record_outbox;
		drop function pgbase_meta.fail_release_a_delete_outbox()`); err != nil {
		t.Fatal(err)
	}
	request.RequestID = "release-a-delete-commit"
	if _, err := records.Delete(ctx, request); err != nil {
		t.Fatalf("delete after injected rollback: %v", err)
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 1, 0, 0)
}

func TestReleaseACascadeRemovesEverySidecarAfterTheBaseClosure(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name:   "release_a_parents",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	child := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "release_a_children",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			deleteRelationField("parent", parent.Name, schema.RelationSingle, schema.RelationCascade),
		},
	})
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	if _, err := membership.New(pool).Cutover(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtime.FleetMember()},
	}); err != nil {
		t.Fatal(err)
	}
	parentResult := createDeleteRecord(t, ctx, records, principal, parent.Name, "release-a-parent", map[string]json.RawMessage{
		"name": json.RawMessage(`"parent"`),
	})
	parentID := recordResultID(t, parentResult)
	childResult := createDeleteRecord(t, ctx, records, principal, child.Name, "release-a-child", map[string]json.RawMessage{
		"name": json.RawMessage(`"child"`), "parent": relationUUIDJSON(parentID),
	})
	childID := recordResultID(t, childResult)
	baseBeforeSidecarGuard := fmt.Sprintf(`create function pgbase_meta.assert_release_a_base_before_sidecar()
		returns trigger language plpgsql as $function$
		begin
			if (select count(*) from pgbase_meta.record_creation_ordinals
				where record_id in ('%s'::uuid, '%s'::uuid)) <> 2 then
				raise exception 'membership sidecar removed before complete base closure';
			end if;
			return old;
		end
		$function$;
		create trigger assert_release_a_parent_order before delete on app.release_a_parents
		for each row execute function pgbase_meta.assert_release_a_base_before_sidecar();
		create trigger assert_release_a_child_order before delete on app.release_a_children
		for each row execute function pgbase_meta.assert_release_a_base_before_sidecar()`, parentID, childID)
	if _, err := pool.Exec(ctx, baseBeforeSidecarGuard); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: "release-a-cascade-delete", Collection: parent.Name, ID: parentID,
		IfMatch: EntityTagSet{Present: true, Values: []string{parentResult.Record.ETag}},
	}); err != nil {
		t.Fatal(err)
	}

	for _, collection := range []schema.Collection{parent, child} {
		var base, sidecar int
		if err := pool.QueryRow(ctx, `select count(*) from app.`+pgx.Identifier{collection.TableName}.Sanitize()).Scan(&base); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_creation_ordinals
			where collection_id = $1`, collection.ID).Scan(&sidecar); err != nil {
			t.Fatal(err)
		}
		if base != 0 || sidecar != 0 {
			t.Fatalf("%s base/sidecar = %d/%d, want 0/0", collection.Name, base, sidecar)
		}
	}
}

func TestReleaseADelayedCreateCommitsBeforeCutoverSnapshotsMembership(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewServiceWithMembershipRuntime(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	controller := membership.New(pool)
	if _, err := pool.Exec(ctx, `create sequence pgbase_meta.release_a_delayed_create_ready;
		create function pgbase_meta.release_a_delay_create()
		returns trigger language plpgsql as $function$
		begin
			if new.title = 'delayed-cutover' then
				perform nextval('pgbase_meta.release_a_delayed_create_ready');
				perform pg_advisory_xact_lock(`+fmt.Sprint(releaseADelayedCreateLockKey)+`);
			end if;
			return new;
		end
		$function$;
		create trigger release_a_delay_create before insert on app.records
		for each row execute function pgbase_meta.release_a_delay_create()`); err != nil {
		t.Fatal(err)
	}

	lockConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Release()
	if _, err := lockConnection.Exec(ctx, `select pg_advisory_lock($1)`, releaseADelayedCreateLockKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			var unlocked bool
			_ = lockConnection.QueryRow(context.Background(), `select pg_advisory_unlock($1)`, releaseADelayedCreateLockKey).Scan(&unlocked)
		}
	}()

	createResult := make(chan r0CreateResult, 1)
	go func() {
		result, createErr := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "release-a-delayed-create", Collection: "records",
			Input: map[string]json.RawMessage{"title": json.RawMessage(`"delayed-cutover"`)},
		})
		createResult <- r0CreateResult{result: result, err: createErr}
	}()
	if err := testsupport.Poll(ctx, 3*time.Second, 5*time.Millisecond, 100*time.Millisecond, func(attempt context.Context) (bool, error) {
		var ready bool
		err := pool.QueryRow(attempt, `select is_called from pgbase_meta.release_a_delayed_create_ready`).Scan(&ready)
		return ready, err
	}); err != nil {
		t.Fatalf("wait for delayed record create: %v", err)
	}

	type cutoverResult struct {
		receipt membership.CutoverReceipt
		err     error
	}
	cutover := make(chan cutoverResult, 1)
	go func() {
		receipt, cutoverErr := controller.Cutover(ctx, membership.FleetProof{
			Members: []membership.FleetMember{runtime.FleetMember()},
		})
		cutover <- cutoverResult{receipt: receipt, err: cutoverErr}
	}()
	select {
	case result := <-cutover:
		t.Fatalf("cutover bypassed the delayed Release-A creation fence: %#v", result)
	case <-time.After(150 * time.Millisecond):
	}
	var unlocked bool
	if err := lockConnection.QueryRow(ctx, `select pg_advisory_unlock($1)`, releaseADelayedCreateLockKey).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("release delayed-create lock = %t, %v", unlocked, err)
	}
	locked = false

	select {
	case result := <-createResult:
		if result.err != nil || result.result.Record == nil {
			t.Fatalf("delayed Create result/error = %#v/%v", result.result, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("delayed Create did not finish: %v", ctx.Err())
	}
	var receipt membership.CutoverReceipt
	select {
	case result := <-cutover:
		if result.err != nil {
			t.Fatal(result.err)
		}
		receipt = result.receipt
	case <-ctx.Done():
		t.Fatalf("cutover did not finish: %v", ctx.Err())
	}
	if receipt.Generation != 1 || receipt.Collections != 1 || receipt.Records != 1 {
		t.Fatalf("cutover receipt = %#v, want one delayed committed record", receipt)
	}
	progress, err := controller.Backfill(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Ready || !progress.Done || progress.Inserted != 1 {
		t.Fatalf("delayed record backfill = %#v", progress)
	}
	var collectionID uuid.UUID
	if err := pool.QueryRow(ctx, `select id from pgbase_meta.collections
		where table_name = 'records'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	assertReleaseAMembershipCounts(t, ctx, pool, collectionID, 1, 1, 1)
}

func assertReleaseAMembershipCounts(
	t *testing.T,
	ctx context.Context,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	collectionID uuid.UUID,
	wantOrdinal int64,
	wantBase int,
	wantSidecar int,
) {
	t.Helper()
	var ordinal int64
	if err := pool.QueryRow(ctx, `select last_ordinal from pgbase_meta.collection_creation_counters
		where collection_id = $1`, collectionID).Scan(&ordinal); err != nil {
		t.Fatal(err)
	}
	var base, sidecar int
	if err := pool.QueryRow(ctx, `select count(*) from app.records`).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_creation_ordinals
		where collection_id = $1`, collectionID).Scan(&sidecar); err != nil {
		t.Fatal(err)
	}
	if ordinal != wantOrdinal || base != wantBase || sidecar != wantSidecar {
		t.Fatalf("ordinal/base/sidecar = %d/%d/%d, want %d/%d/%d",
			ordinal, base, sidecar, wantOrdinal, wantBase, wantSidecar)
	}
}
