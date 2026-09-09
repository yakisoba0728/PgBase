//go:build integration

package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRecordOperationsLegacyPromotionPreconditionsAndAtomicArtifacts(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	id := uuid.MustParse("019551d0-e9c0-7000-8000-000000000099")
	if _, err := pool.Exec(ctx, `INSERT INTO app.records (id, created_at, updated_at, title, count, metadata)
		VALUES ($1, transaction_timestamp(), transaction_timestamp(), 'legacy-secret-value', 1, '{"n":9007199254740993}'::jsonb)`, id); err != nil {
		t.Fatal(err)
	}

	legacy, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: testRequestID, Collection: "records", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Version != 0 || !strings.HasPrefix(legacy.ETag, `"pgb0.`) {
		t.Fatalf("legacy record=%+v", legacy)
	}
	var revisions int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_revisions").Scan(&revisions); err != nil || revisions != 0 {
		t.Fatalf("GET wrote sidecar count=%d err=%v", revisions, err)
	}

	updated, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records", ID: id,
		Input:   map[string]json.RawMessage{"count": json.RawMessage(`2`)},
		IfMatch: EntityTagSet{Present: true, Values: []string{legacy.ETag}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Record == nil || updated.Record.Version != 1 || !strings.HasPrefix(updated.Record.ETag, `"pgb1.`) {
		t.Fatalf("promoted result=%+v", updated)
	}

	_, err = records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "request_stale_1", Collection: "records", ID: id,
		Input:   map[string]json.RawMessage{"count": json.RawMessage(`3`)},
		IfMatch: EntityTagSet{Present: true, Values: []string{legacy.ETag}},
	})
	var precondition *Error
	if !errors.As(err, &precondition) || precondition.Code != CodePreconditionFailed || precondition.CurrentETag != updated.Record.ETag {
		t.Fatalf("stale update error=%#v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count FROM app.records WHERE id=$1", id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("stale update changed row count=%d err=%v", count, err)
	}
	var auditCount, outboxCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_audit_events WHERE record_id=$1", id).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_outbox WHERE record_id=$1", id).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || outboxCount != 1 {
		t.Fatalf("artifacts audit=%d outbox=%d want 1/1", auditCount, outboxCount)
	}
}

func TestRecordOperationsRejectInvalidPatchBeforeEvaluatingStalePrecondition(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"validation-order"`)},
		IdempotencyKey: "validation-order-key-123456",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "request_invalid_patch", Collection: "records",
		ID:      uuid.MustParse(created.Record.Data["id"].(string)),
		Input:   map[string]json.RawMessage{"unknown": json.RawMessage(`"value"`)},
		IfMatch: EntityTagSet{Present: true, Values: []string{`"pgb1.stale"`}},
	})
	var validation *query.ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 ||
		validation.Details[0].Field != "unknown" || validation.Details[0].Code != "unknown_field" {
		t.Fatalf("Update(stale ETag, invalid patch) error = %#v, want unknown_field validation", err)
	}
}

func TestRecordOperationsRejectMalformedPatchBeforeMalformedPrecondition(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	_, err := records.Update(context.Background(), UpdateRequest{
		Principal: principal, RequestID: "request_malformed_patch", Collection: "records",
		ID:      uuid.New(),
		Input:   map[string]json.RawMessage{"title": json.RawMessage(`{`)},
		IfMatch: EntityTagSet{Present: true},
	})
	var validation *query.ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 ||
		validation.Details[0].Field != "title" || validation.Details[0].Code != "type_mismatch" {
		t.Fatalf("Update(malformed patch/precondition) error = %#v, want title type_mismatch", err)
	}
}

func TestRecordOperationsRejectTypeMismatchBeforeMalformedPrecondition(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	_, err := records.Update(context.Background(), UpdateRequest{
		Principal: principal, RequestID: "request_type_mismatch", Collection: "records",
		ID:      uuid.New(),
		Input:   map[string]json.RawMessage{"title": json.RawMessage(`123`)},
		IfMatch: EntityTagSet{Present: true},
	})
	var validation *query.ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 ||
		validation.Details[0].Field != "title" || validation.Details[0].Code != "type_mismatch" {
		t.Fatalf("Update(type mismatch/precondition) error = %#v, want title type_mismatch", err)
	}
}

func TestRecordOperationsCreateReplayConflictAndDataMinimization(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	key := "idempotency-key-1234567890"
	input := map[string]json.RawMessage{
		"title":    json.RawMessage(`"private-record-value"`),
		"metadata": json.RawMessage(`{"large":9007199254740993}`),
	}
	request := CreateRequest{Principal: principal, RequestID: testRequestID, Collection: "records", Input: input, IdempotencyKey: key}
	first, err := records.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := records.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || !replay.Replayed || first.EventID != replay.EventID || first.Record.ETag != replay.Record.ETag {
		t.Fatalf("first=%+v replay=%+v", first, replay)
	}
	firstJSON, _ := json.Marshal(first.Record.Data)
	replayJSON, _ := json.Marshal(replay.Record.Data)
	if !bytes.Equal(firstJSON, replayJSON) || !bytes.Contains(replayJSON, []byte("9007199254740993")) {
		t.Fatalf("replay body mismatch or precision loss: %s / %s", firstJSON, replayJSON)
	}
	var defaultedCount string
	if err := pool.QueryRow(ctx, `SELECT envelope->'changes'->>'count'
		FROM pgbase_meta.record_outbox WHERE event_id=$1`, first.EventID).Scan(&defaultedCount); err != nil {
		t.Fatal(err)
	}
	if defaultedCount != "0" {
		t.Fatalf("normalized committed field missing from create event: count=%q", defaultedCount)
	}

	request.Input = map[string]json.RawMessage{"title": json.RawMessage(`"different"`)}
	if _, err := records.Create(ctx, request); !IsCode(err, CodeIdempotencyConflict) {
		t.Fatalf("different replay error=%v", err)
	}

	for table, want := range map[string]int{
		"app.records": 1, "pgbase_meta.record_revisions": 1,
		"pgbase_meta.record_audit_events": 1, "pgbase_meta.record_outbox": 1,
		"pgbase_meta.record_idempotency": 1,
	} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v want %d", table, count, err, want)
		}
	}
	var auditText string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(event)::text FROM pgbase_meta.record_audit_events event").Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{key, "private-record-value", "recordops@example.test", "session-secret"} {
		if strings.Contains(auditText, secret) {
			t.Fatalf("audit contains secret %q: %s", secret, auditText)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE pgbase_meta.record_audit_events SET request_id='changed'"); err == nil {
		t.Fatal("append-only audit allowed UPDATE")
	}
}

func TestRecordOperationsRollbackWhenOutboxWriteFails(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.fail_outbox_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
		CREATE TRIGGER fail_outbox BEFORE INSERT ON pgbase_meta.record_outbox
		FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_outbox_insert()`); err != nil {
		t.Fatal(err)
	}
	_, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"rollback"`)},
		IdempotencyKey: "rollback-key-123456789",
	})
	if err == nil {
		t.Fatal("create succeeded through injected outbox failure")
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox", "pgbase_meta.record_idempotency"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v want rollback", table, count, err)
		}
	}
}

func TestRecordOperationsDoNotReturnAnUncommittedMutationResult(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.fail_record_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected deferred commit failure'; END $$;
		CREATE CONSTRAINT TRIGGER fail_record_commit
		AFTER INSERT ON app.records
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_record_commit()`); err != nil {
		t.Fatal(err)
	}
	result, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"commit-failure"`)},
		IdempotencyKey: "commit-failure-key-123456",
	})
	if err == nil {
		t.Fatal("create succeeded through a deferred commit failure")
	}
	if result.Record != nil || result.EventID != uuid.Nil || result.Replayed || result.Compatibility != CompatibilityNone {
		t.Fatalf("Create returned an uncommitted result with error: %+v / %v", result, err)
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox", "pgbase_meta.record_idempotency"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v want rollback", table, count, err)
		}
	}
}

func TestRecordOperationsMultiPoolIdempotencyAndStaleWrite(t *testing.T) {
	databaseURL, pool, schemas, firstService, principal := newRecordOperationsTestWithURL(t)
	ctx := context.Background()
	secondPool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secondPool.Close)
	secondSchemas := schema.NewService(secondPool, &schema.Cache{}, nil)
	secondService := NewService(secondPool, secondSchemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, nil)

	request := CreateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"once"`)},
		IdempotencyKey: "concurrent-key-12345678",
	}
	start := make(chan struct{})
	results := make([]MutationResult, 2)
	errs := make([]error, 2)
	services := []*Service{firstService, secondService}
	var wait sync.WaitGroup
	for index := range services {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			local := request
			local.RequestID = "concurrent_request_" + string(rune('a'+index))
			results[index], errs[index] = services[index].Create(ctx, local)
		}(index)
	}
	close(start)
	wait.Wait()
	successes := 0
	for index, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !IsCode(err, CodeIdempotencyInProgress) {
			t.Fatalf("create[%d] error=%v", index, err)
		}
	}
	if successes == 0 {
		t.Fatalf("no concurrent create succeeded: %v", errs)
	}
	after, err := secondService.Create(ctx, request)
	if err != nil || !after.Replayed {
		t.Fatalf("post-concurrency replay=%+v err=%v", after, err)
	}
	var recordCount, eventCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.records").Scan(&recordCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_audit_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if recordCount != 1 || eventCount != 1 {
		t.Fatalf("concurrent create records/events=%d/%d", recordCount, eventCount)
	}

	loaded, err := firstService.Get(ctx, GetRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records", ID: uuid.MustParse(after.Record.Data["id"].(string)),
	})
	if err != nil {
		t.Fatal(err)
	}
	updateRequests := []UpdateRequest{
		{Principal: principal, RequestID: "concurrent_update_a", Collection: "records", ID: uuid.MustParse(loaded.Data["id"].(string)), Input: map[string]json.RawMessage{"count": json.RawMessage(`1`)}, IfMatch: EntityTagSet{Present: true, Values: []string{loaded.ETag}}},
		{Principal: principal, RequestID: "concurrent_update_b", Collection: "records", ID: uuid.MustParse(loaded.Data["id"].(string)), Input: map[string]json.RawMessage{"count": json.RawMessage(`2`)}, IfMatch: EntityTagSet{Present: true, Values: []string{loaded.ETag}}},
	}
	updateErrs := make([]error, 2)
	start = make(chan struct{})
	for index := range services {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, updateErrs[index] = services[index].Update(ctx, updateRequests[index])
		}(index)
	}
	close(start)
	wait.Wait()
	updateSuccesses, stale := 0, 0
	for _, err := range updateErrs {
		if err == nil {
			updateSuccesses++
		} else if IsCode(err, CodePreconditionFailed) {
			stale++
		} else {
			t.Fatalf("unexpected concurrent update error=%v", err)
		}
	}
	if updateSuccesses != 1 || stale != 1 {
		t.Fatalf("concurrent updates successes/stale=%d/%d errors=%v", updateSuccesses, stale, updateErrs)
	}
	_ = schemas
}

func TestIdempotencyAdvisoryLockPrecedesCatalogSnapshot(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	key := "lock-order-key-12345678"
	keyDigest := recordartifact.Digest("pgbase-idempotency-key-v1", []byte(key))
	lockKey := advisoryLockKey(principal, []byte("records"), keyDigest[:])

	// The membership runtime is registered before the gate below because its
	// own catalog pin is not part of the ordering under test.
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterB)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		t.Fatal(err)
	}
	// EnsureCurrent pins the catalog with SELECT ... FOR SHARE on the singleton
	// schema_state row. Holding that row FOR UPDATE makes the pin block, so a
	// create that reached the catalog before the idempotency lock cannot answer
	// in-progress within the deadline below.
	if _, err := tx.Exec(ctx, "SELECT version FROM pgbase_meta.schema_state WHERE singleton FOR UPDATE"); err != nil {
		t.Fatal(err)
	}

	lockedContext, cancelLocked := context.WithTimeout(ctx, 3*time.Second)
	_, err = records.Create(lockedContext, CreateRequest{
		Principal: principal, RequestID: testRequestID, Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"lock-order"`)},
		IdempotencyKey: key,
	})
	cancelLocked()
	if !IsCode(err, CodeIdempotencyInProgress) {
		t.Fatalf("create while lock held error=%v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "request_after_lock", Collection: "records",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"lock-order"`)},
		IdempotencyKey: key,
	}); err != nil {
		t.Fatalf("create after lock release: %v", err)
	}
}

func TestIdempotencyCleanerUsesBoundedPasses(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		_, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "cleaner_request_" + string(rune('a'+index)), Collection: "records",
			Input:          map[string]json.RawMessage{"title": json.RawMessage(`"cleanup-` + string(rune('a'+index)) + `"`)},
			IdempotencyKey: "cleanup-key-1234567" + string(rune('a'+index)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_idempotency
		SET created_at=transaction_timestamp()-interval '48 hours',
			expires_at=transaction_timestamp()-interval '24 hours'`); err != nil {
		t.Fatal(err)
	}
	cleaner := NewIdempotencyCleaner(pool)
	cleaner.batchSize = 2
	first, err := cleaner.Clean(ctx)
	if err != nil || first != 2 {
		t.Fatalf("first clean=%d err=%v", first, err)
	}
	second, err := cleaner.Clean(ctx)
	if err != nil || second != 1 {
		t.Fatalf("second clean=%d err=%v", second, err)
	}
	for index := 0; index < 5; index++ {
		_, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "drain_request_" + string(rune('a'+index)), Collection: "records",
			Input:          map[string]json.RawMessage{"title": json.RawMessage(`"drain-` + string(rune('a'+index)) + `"`)},
			IdempotencyKey: "drain-cleanup-key-123" + string(rune('a'+index)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.record_idempotency
		SET created_at=transaction_timestamp()-interval '48 hours',
			expires_at=transaction_timestamp()-interval '24 hours'`); err != nil {
		t.Fatal(err)
	}
	cleaner.interval = time.Hour
	cleaner.batchYield = 0
	runCtx, stop := context.WithCancel(ctx)
	runResult := make(chan error, 1)
	go func() { runResult <- cleaner.Run(runCtx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var remaining int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_idempotency").Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run left %d expired rows after consecutive full batches", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if err := <-runResult; err != nil {
		t.Fatalf("Run() after drain=%v", err)
	}
	cleaner.batchYield = -time.Nanosecond
	if err := cleaner.Run(ctx); err == nil {
		t.Fatal("Run accepted a negative inter-batch yield")
	}
	cleaner.batchYield = 0
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := cleaner.Run(canceled); err != nil {
		t.Fatalf("Run(canceled)=%v", err)
	}
}

// TestRelationReadTelemetrySortSamplesFollowTheDottedPath covers what the
// request text alone cannot answer. Only a dotted sort reads a target, so only
// it leaves a relation_read sample -- and it leaves one whether the planner
// accepts it or rejects it, because that rejection is the relation work being
// measured. A bare single relation orders on the root's own foreign-key column
// and a bare multi relation is refused from its cardinality, so neither is
// relation-path work any more than the scalar sort beside them.
func TestRelationReadTelemetrySortSamplesFollowTheDottedPath(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)

	recorder := &relationTelemetryRecorder{}
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, recorder, nil)
	testutil.RetireRecordMembershipV4(t, ctx, pool)
	for _, test := range []struct {
		sort      string
		wantError bool
	}{
		{sort: "-created_at"},
		{sort: "owner"},
		{sort: "links", wantError: true},
	} {
		_, err := records.List(ctx, ListRequest{
			Principal: principal, RequestID: testRequestID, Collection: "null_survivors", Sort: test.sort,
		})
		if (err != nil) != test.wantError {
			t.Fatalf("List(sort=%q) error = %v, want error %t", test.sort, err, test.wantError)
		}
	}
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("root-only sort telemetry = %#v, want no relation samples", events)
	}

	// null_targets.name is not indexed, so the planner refuses this dotted sort
	// after resolving the path -- the sample still has to be there.
	if _, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: testRequestID, Collection: "null_survivors", Sort: "owner.name",
	}); err == nil {
		t.Fatal("List(sort=owner.name) error = nil, want the unindexed target rejection")
	}
	events := recorder.snapshot()
	if len(events) != 1 {
		t.Fatalf("dotted sort telemetry = %#v, want one relation sample", events)
	}
	if events[0].Component() != observability.ComponentRelation ||
		events[0].Operation() != observability.OperationRelationRead ||
		events[0].Outcome() != observability.OutcomeValidation {
		t.Fatalf("dotted sort telemetry = %#v, want relation_read/validation", events[0])
	}
}

// TestRelationAwareRecordPageStaysWithinTheAdvertisedByteBudget drives an
// oversized page through both relation-aware readers end to end. Five ~900 KB
// records overflow the 48 MiB bound, so the row scan stops short, and whatever
// each reader then emits -- envelope, separators and continuation cursor
// included -- has to measure under the bound and still hand back every record
// across the two pages. This pins the readers' page accounting from the
// outside; it reaches the scan budget rather than the finalize trim, which
// only fires in the narrow window where the envelope alone crosses the bound
// and needs the direct unit test noted in the handoff.
func TestRelationAwareRecordPageStaysWithinTheAdvertisedByteBudget(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, nil)

	// query.maximumRecordPageBytes is package-private; this is the same bound
	// the API contract advertises, and expansion reuses it as its own default.
	const maximumPageBytes = 48<<20 + 4<<10
	const recordCount = 60
	filler, err := json.Marshal(strings.Repeat("x", 900_000))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, recordCount)
	for index := range recordCount {
		result, createErr := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: fmt.Sprintf("page-budget-%04d", index),
			Collection: "null_survivors", Input: map[string]json.RawMessage{"name": filler},
		})
		if createErr != nil {
			t.Fatalf("create oversized record %d: %v", index, createErr)
		}
		want = append(want, recordResultID(t, result).String())
	}
	sort.Strings(want)
	testutil.RetireRecordMembershipV4(t, ctx, pool)

	// sort=owner drives the relation list kernel; adding expand drives the
	// expansion reader. Both must trim the same way.
	for _, expand := range []string{"", "owner"} {
		request := ListRequest{
			Principal: principal, RequestID: testRequestID, Collection: "null_survivors",
			Sort: "owner", Limit: recordCount, Expand: expand,
		}
		page, listErr := records.List(ctx, request)
		if listErr != nil {
			t.Fatalf("expand=%q: List(): %v", expand, listErr)
		}
		if len(page.Items) == 0 || len(page.Items) >= recordCount {
			t.Fatalf("expand=%q: page items = %d, want a page trimmed below %d", expand, len(page.Items), recordCount)
		}
		if page.NextCursor == "" {
			t.Fatalf("expand=%q: trimmed page carries no continuation cursor", expand)
		}
		encoded, marshalErr := json.Marshal(page)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if len(encoded)+1 > maximumPageBytes {
			t.Fatalf("expand=%q: emitted page = %d bytes, want at most %d", expand, len(encoded)+1, maximumPageBytes)
		}

		got := make([]string, 0, recordCount)
		for cursor := ""; ; {
			request.Cursor = cursor
			next, pageErr := records.List(ctx, request)
			if pageErr != nil {
				t.Fatalf("expand=%q: List(cursor): %v", expand, pageErr)
			}
			for _, item := range next.Items {
				id, ok := item["id"].(string)
				if !ok {
					t.Fatalf("expand=%q: record id = %#v", expand, item["id"])
				}
				got = append(got, id)
			}
			if next.NextCursor == "" {
				break
			}
			cursor = next.NextCursor
			if len(got) > recordCount {
				t.Fatalf("expand=%q: pagination returned %d records, want %d", expand, len(got), recordCount)
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("expand=%q: paginated ids = %v, want %v", expand, got, want)
		}
	}
}

func TestDeleteGraphDerivedUpdateStampsStatementTime(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)

	target := recordResultID(t, createDeleteRecord(t, ctx, records, principal, "null_targets", "monotonic-target",
		map[string]json.RawMessage{"name": json.RawMessage(`"target"`)}))
	survivor := recordResultID(t, createDeleteRecord(t, ctx, records, principal, "null_survivors", "monotonic-source",
		map[string]json.RawMessage{"name": json.RawMessage(`"survivor"`), "owner": relationUUIDJSON(target)}))

	requestID := "delete-monotonic-0001"
	if _, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: target,
	}); err != nil {
		t.Fatalf("delete set-null target: %v", err)
	}

	// committed_at is the delete transaction's transaction_timestamp(). The
	// derived update runs many statements later, so only a statement_timestamp()
	// stamp is strictly newer than the transaction start.
	var updatedAt, transactionStart time.Time
	if err := pool.QueryRow(ctx, `SELECT s.updated_at, a.committed_at
		FROM app.null_survivors s, pgbase_meta.record_audit_events a
		WHERE s.id = $1 AND a.record_id = $1 AND a.request_id = $2 AND a.operation = 'update'`,
		survivor, requestID).Scan(&updatedAt, &transactionStart); err != nil {
		t.Fatal(err)
	}
	if !updatedAt.After(transactionStart) {
		t.Fatalf("derived updated_at=%s is not newer than the delete transaction start=%s", updatedAt, transactionStart)
	}
}
