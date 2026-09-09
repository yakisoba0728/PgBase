//go:build integration

package sqlconsole

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRunnerReturnsExactTaggedValuesAndHardResultLimits(t *testing.T) {
	fixture := newRunnerFixture(t)

	exact := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: `select
		9007199254740993::numeric as big,
		1.2300::numeric as amount,
		'{"nested":{"value":9007199254740993},"missing":null}'::jsonb as document,
		null::text as missing,
		''::text as empty`}}}
	result, err := fixture.run(context.Background(), exact)
	if err != nil {
		t.Fatalf("run exact query: %v", err)
	}
	want := []Cell{
		{Kind: CellDecimal, Text: "9007199254740993"},
		{Kind: CellDecimal, Text: "1.2300"},
		{Kind: CellJSON, Text: `{"nested":{"value":9007199254740993},"missing":null}`},
		{Kind: CellNull},
		{Kind: CellString, Text: ""},
	}
	if got := result.Statements[0].Rows[0]; !sameCells(got, want) {
		t.Fatalf("exact row = %#v", got)
	}
	if result.Committed == nil || *result.Committed {
		t.Fatalf("read committed = %#v", result.Committed)
	}

	rows := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{
		SQL: "select n from pg_catalog.generate_series(1,1001) as n",
	}}}
	if _, err := fixture.run(context.Background(), rows); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("1,001 rows error = %v", err)
	}

	columns := make([]string, 129)
	for index := range columns {
		columns[index] = "1 as c" + string(rune('a'+index%26)) + string(rune('a'+index/26))
	}
	tooManyColumns := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{
		SQL: "select " + strings.Join(columns, ","),
	}}}
	if _, err := fixture.run(context.Background(), tooManyColumns); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("129 columns error = %v", err)
	}

	largeID := uuid.New()
	if _, err := fixture.pool.owner.Exec(context.Background(), `insert into ext.items(id,amount,label) values($1,1,$2)`,
		largeID, strings.Repeat("x", MaximumResultBytes)); err != nil {
		t.Fatal(err)
	}
	tooManyBytes := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{
		SQL:        "select label from ext.items where id=$1",
		Parameters: []Parameter{{Kind: ParameterUUID, Text: largeID.String()}},
	}}}
	if _, err := fixture.run(context.Background(), tooManyBytes); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("result bytes error = %v", err)
	}
}

func TestRunnerCommitsMultipleWritesAndReconcilesLostCommitResponse(t *testing.T) {
	fixture := newRunnerFixture(t)
	itemID := uuid.New()
	request := writeRequest(uuid.New(), "write-success-000000000001", itemID)

	fixture.runner.commit = func(ctx context.Context, transaction pgx.Tx) error {
		if err := transaction.Commit(ctx); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	result, err := fixture.run(context.Background(), request)
	if err != nil {
		t.Fatalf("run committed write: %v", err)
	}
	if result.Committed == nil || !*result.Committed || result.AffectedRows != 2 || result.Rows != 1 {
		t.Fatalf("write result = %#v", result)
	}
	var amount string
	if err := fixture.pool.owner.QueryRow(context.Background(), `select amount::text from ext.items where id=$1`, itemID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != "3.7500" {
		t.Fatalf("amount = %q", amount)
	}
	var receipts int
	if err := fixture.pool.owner.QueryRow(context.Background(), `select count(*) from pgbase_meta.sql_console_write_receipts where execution_id=$1`, request.ExecutionID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("receipts = %d", receipts)
	}
}

func TestRunnerMarksTransportCommitWithoutReceiptUnknown(t *testing.T) {
	fixture := newRunnerFixture(t)
	itemID := uuid.New()
	request := writeRequest(uuid.New(), "write-ambiguous-0000000001", itemID)
	fixture.runner.commit = func(context.Context, pgx.Tx) error { return io.ErrUnexpectedEOF }

	result, err := fixture.run(context.Background(), request)
	if !errors.Is(err, ErrCommitUnknown) {
		t.Fatalf("ambiguous commit error = %v", err)
	}
	if result.Committed != nil || result.AffectedRows != 2 || result.Rows != 1 {
		t.Fatalf("ambiguous result = %#v", result)
	}
	assertItemAbsent(t, fixture.pool, itemID)
}

func TestRunnerRollsBackFailedDryRunAndAffectedLimitWrites(t *testing.T) {
	fixture := newRunnerFixture(t)

	failedID := uuid.New()
	failed := Request{
		ExecutionID: failedID, Mode: ModeWrite, IdempotencyKey: "write-failure-000000000001",
		Statements: []Statement{
			insertItemStatement(failedID, "1.0", "first"),
			insertItemStatement(failedID, "2.0", "duplicate"),
		},
	}
	if _, err := fixture.run(context.Background(), failed); err == nil || SQLState(err) != "23505" {
		t.Fatalf("second statement error = %v, SQLSTATE %q", err, SQLState(err))
	}
	assertItemAbsent(t, fixture.pool, failedID)

	dryID := uuid.New()
	dryRun := writeRequest(uuid.New(), "write-dry-run-000000000001", dryID)
	dryRun.DryRun = true
	result, err := fixture.run(context.Background(), dryRun)
	if err != nil {
		t.Fatalf("run dry-run: %v", err)
	}
	if result.Committed == nil || *result.Committed {
		t.Fatalf("dry-run committed = %#v", result.Committed)
	}
	assertItemAbsent(t, fixture.pool, dryID)

	if _, err := fixture.pool.owner.Exec(context.Background(), `insert into ext.items(id,amount,label)
		select pg_catalog.lpad(pg_catalog.to_hex(n),32,'0')::uuid,1,'before'
		from pg_catalog.generate_series(1,1001) n`); err != nil {
		t.Fatal(err)
	}
	affected := Request{
		ExecutionID: uuid.New(), Mode: ModeWrite, IdempotencyKey: "write-affected-cap-000000001",
		Statements: []Statement{{SQL: "update ext.items set label='changed'"}},
	}
	if _, err := fixture.run(context.Background(), affected); !errors.Is(err, ErrAffectedLimit) {
		t.Fatalf("affected cap error = %v", err)
	}
	var changed int
	if err := fixture.pool.owner.QueryRow(context.Background(), `select count(*) from ext.items where label='changed'`).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatalf("affected-cap rollback left %d changed rows", changed)
	}
}

func TestRunnerRevalidatesTargetShapeAfterStartup(t *testing.T) {
	fixture := newRunnerFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.owner.Exec(ctx, `create function ext.touch_managed_late() returns trigger
		language plpgsql as $$begin update app.accounts set visible='changed'; return new; end$$;
		create trigger items_touch_late before update on ext.items for each row execute function ext.touch_managed_late()`); err != nil {
		t.Fatal(err)
	}
	itemID := uuid.New()
	request := writeRequest(uuid.New(), "write-shape-race-00000000001", itemID)
	if _, err := fixture.run(ctx, request); !errors.Is(err, ErrTargetChanged) {
		t.Fatalf("changed target error = %v", err)
	}
	assertItemAbsent(t, fixture.pool, itemID)
}

type runnerFixture struct {
	pool   poolFixture
	runner *Runner
	secret []byte
}

func newRunnerFixture(t *testing.T) runnerFixture {
	t.Helper()
	pool := newPoolFixture(t)
	pools, err := OpenPools(context.Background(), pool.options)
	if err != nil {
		t.Fatalf("OpenPools: %v", err)
	}
	t.Cleanup(pools.Close)
	runner, err := NewRunner(pools, pool.owner)
	if err != nil {
		t.Fatal(err)
	}
	return runnerFixture{pool: pool, runner: runner, secret: []byte("n8-runner-test-secret-that-is-long-enough")}
}

func (fixture runnerFixture) run(ctx context.Context, request Request) (Result, error) {
	requestHash := RequestDigest(fixture.secret, request)
	idempotencyHash := sha256.Sum256([]byte(request.IdempotencyKey))
	var idempotency any
	if request.Mode == ModeWrite {
		idempotency = idempotencyHash[:]
	}
	queryHash := QueryFingerprint(fixture.secret, request.Statements)
	_, err := fixture.pool.owner.Exec(ctx, `insert into pgbase_meta.sql_console_executions
		(execution_id,administrator_id,mode,dry_run,idempotency_hash,request_hash,query_fingerprint,status,
		statement_count,created_at,updated_at,started_at)
		values($1,$2,$3,$4,$5,$6,$7,'running',$8,current_timestamp,current_timestamp,current_timestamp)`,
		request.ExecutionID, fixture.pool.actorID, request.Mode, request.DryRun, idempotency,
		requestHash[:], queryHash[:], len(request.Statements))
	if err != nil {
		return Result{}, err
	}
	return fixture.runner.Run(ctx, RunInput{
		AdministratorID: fixture.pool.actorID,
		Request:         request,
		RequestHash:     requestHash,
		IdempotencyHash: idempotencyHash,
	})
}

func writeRequest(executionID uuid.UUID, key string, itemID uuid.UUID) Request {
	return Request{
		ExecutionID: executionID, Mode: ModeWrite, IdempotencyKey: key,
		Statements: []Statement{
			insertItemStatement(itemID, "1.2500", "created"),
			{
				SQL: "update ext.items set amount=amount+$1 where id=$2 returning amount,label",
				Parameters: []Parameter{
					{Kind: ParameterDecimal, Text: "2.5000"},
					{Kind: ParameterUUID, Text: itemID.String()},
				},
			},
		},
	}
}

func insertItemStatement(itemID uuid.UUID, amount, label string) Statement {
	return Statement{
		SQL: "insert into ext.items(id,amount,label) values($1,$2,$3)",
		Parameters: []Parameter{
			{Kind: ParameterUUID, Text: itemID.String()},
			{Kind: ParameterDecimal, Text: amount},
			{Kind: ParameterString, Text: label},
		},
	}
}

func assertItemAbsent(t *testing.T, fixture poolFixture, itemID uuid.UUID) {
	t.Helper()
	var count int
	if err := fixture.owner.QueryRow(context.Background(), `select count(*) from ext.items where id=$1`, itemID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("item %s survived rollback", itemID)
	}
}

func sameCells(left, right []Cell) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestRunnerTimeoutCancelsAndDiscardsOnlyItsConnection(t *testing.T) {
	fixture := newRunnerFixture(t)
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Timeout: 50 * time.Millisecond, Statements: []Statement{{
		SQL: "select pg_catalog.pg_sleep(10)",
	}}}
	if _, err := fixture.run(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) && SQLState(err) != "57014" {
		t.Fatalf("timeout error = %v", err)
	}

	next := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	if _, err := fixture.run(context.Background(), next); err != nil {
		t.Fatalf("query after canceled connection: %v", err)
	}
}
