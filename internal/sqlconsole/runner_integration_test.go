//go:build integration

package sqlconsole

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
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

func TestRunnerPinsSupportedSchemaViewGenerationAfterCatalogCheck(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	owner, err := database.Open(ctx, databaseURL, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	if err := migrate.New(owner, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administratorID := uuid.New()
	if _, err := owner.Exec(ctx, `insert into pgbase_meta.admins
		(id,email,password_hash,active,created_at,updated_at)
		values($1,$2,'n8-test',true,current_timestamp,current_timestamp)`,
		administratorID, administratorID.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}

	schemaService := schema.NewService(owner, &schema.Cache{}, nil)
	sourceInput := schema.CreateCollectionInput{Name: "generation_source", Fields: []schema.FieldInput{
		{Name: "public_value", Type: schema.TypeText},
		{Name: "private_value", Type: schema.TypeText, Hidden: true},
	}}
	sourcePlan, err := schemaService.Preview(ctx, schema.NewCreateChange(sourceInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemaService.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(sourceInput), sourcePlan, administratorID)); err != nil {
		t.Fatal(err)
	}
	oldSQL := `select id,created_at,updated_at,public_value as exposed from app.generation_source`
	viewInput := schema.CreateCollectionInput{Name: "generation_view", View: &schema.ViewInput{SQL: oldSQL}}
	viewPlan, err := schemaService.Preview(ctx, schema.NewCreateChange(viewInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemaService.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(viewInput), viewPlan, administratorID)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `insert into app.generation_source
		(id,created_at,updated_at,public_value,private_value)
		values($1,current_timestamp,current_timestamp,'public-generation','private-generation')`, uuid.New()); err != nil {
		t.Fatal(err)
	}

	catalog, err := schemaService.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roles := provisionGenerationFenceRoles(t, owner, databaseURL)
	var versionCalls atomic.Int64
	var blockCall atomic.Int64
	versionChecked := make(chan int64, 1)
	resumeVersionCheck := make(chan struct{})
	catalogVersion := func(callContext context.Context) (int64, error) {
		version, versionErr := schema.NewStore(owner).SchemaVersion(callContext)
		call := versionCalls.Add(1)
		if versionErr == nil && call == blockCall.Load() {
			versionChecked <- version
			select {
			case <-resumeVersionCheck:
			case <-callContext.Done():
				return 0, callContext.Err()
			}
		}
		return version, versionErr
	}
	pools, err := OpenPools(ctx, PoolOptions{
		CompilerURL:    roles.compilerURL,
		ReadURL:        roles.readURL,
		WriterURL:      roles.writerURL,
		Catalog:        catalog,
		CatalogVersion: catalogVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pools.Close)
	runner, err := NewRunner(pools, owner)
	if err != nil {
		t.Fatal(err)
	}
	fixture := runnerFixture{pool: poolFixture{owner: owner, actorID: administratorID}, runner: runner, secret: []byte("n8-generation-fence-test-secret")}

	newSQL := `select id,created_at,updated_at,private_value as exposed from app.generation_source`
	update := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "generation_view", View: &schema.ViewInput{SQL: newSQL}})
	updatePlan, err := schemaService.Preview(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	blockCall.Store(versionCalls.Load() + 2) // ValidatePlan, then the in-transaction generation check.
	type runOutcome struct {
		result Result
		err    error
	}
	runDone := make(chan runOutcome, 1)
	go func() {
		request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Timeout: 10 * time.Second,
			Statements: []Statement{{SQL: `select exposed from app.generation_view`}}}
		result, runErr := fixture.run(ctx, request)
		runDone <- runOutcome{result: result, err: runErr}
	}()
	select {
	case version := <-versionChecked:
		if version != catalog.Version {
			t.Fatalf("checked version = %d, want %d", version, catalog.Version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not reach the post-transaction catalog check")
	}

	applyDone := make(chan error, 1)
	go func() {
		_, applyErr := schemaService.Apply(ctx, schema.LegacyApply(update, updatePlan, administratorID))
		applyDone <- applyErr
	}()
	applyErr, appliedBeforeResume := waitForApplyOrAdvisoryWait(t, owner, applyDone)
	close(resumeVersionCheck)
	var outcome runOutcome
	select {
	case outcome = <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not finish after the catalog check resumed")
	}
	if !appliedBeforeResume {
		select {
		case applyErr = <-applyDone:
		case <-time.After(5 * time.Second):
			t.Fatal("schema update did not finish after the read released its generation lock")
		}
	}
	if applyErr != nil {
		t.Fatalf("apply supported view update: %v", applyErr)
	}
	if outcome.err != nil {
		t.Fatalf("run generation-pinned read: %v", outcome.err)
	}
	if len(outcome.result.Statements) != 1 || len(outcome.result.Statements[0].Rows) != 1 ||
		len(outcome.result.Statements[0].Rows[0]) != 1 || outcome.result.Statements[0].Rows[0][0].Text != "public-generation" {
		t.Fatalf("read crossed catalog generation: %#v", outcome.result)
	}
	var currentVersion int64
	if currentVersion, err = schema.NewStore(owner).SchemaVersion(ctx); err != nil || currentVersion != catalog.Version+1 {
		t.Fatalf("schema version after update = %d, err=%v", currentVersion, err)
	}
	stale := Request{ExecutionID: uuid.New(), Mode: ModeRead,
		Statements: []Statement{{SQL: `select exposed from app.generation_view`}}}
	if _, err := fixture.run(ctx, stale); !errors.Is(err, ErrCatalogChanged) {
		t.Fatalf("read after generation release error = %v, want ErrCatalogChanged", err)
	}
}

type generationFenceRoles struct {
	compilerURL string
	readURL     string
	writerURL   string
}

func provisionGenerationFenceRoles(t *testing.T, owner *database.Pool, databaseURL string) generationFenceRoles {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	compilerRole := "n8_generation_compiler_" + suffix
	readRole := "n8_generation_read_" + suffix
	writerRole := "n8_generation_writer_" + suffix
	password := "n8-generation-password-" + suffix
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	identifiers := []string{
		pgx.Identifier{compilerRole}.Sanitize(),
		pgx.Identifier{readRole}.Sanitize(),
		pgx.Identifier{writerRole}.Sanitize(),
	}
	if _, err := owner.Exec(context.Background(), fmt.Sprintf(`
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		revoke all on database %s from public;
		revoke all on schema app from public;
		grant connect,temp on database %s to %s;
		grant connect on database %s to %s,%s;
		grant usage on schema app to %s,%s,%s;
		grant usage on schema pgbase_meta to %s;
		grant select (id,created_at,updated_at,public_value) on app.generation_source to %s,%s,%s;
		grant select on app.generation_view to %s,%s,%s;
		grant insert (execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
			on pgbase_meta.sql_console_write_receipts to %s;
		alter default privileges in schema app grant select on tables to %s,%s,%s;
		alter role %s in database %s set default_transaction_read_only=on`,
		identifiers[0], quoteLiteral(password), identifiers[1], quoteLiteral(password), identifiers[2], quoteLiteral(password),
		pgx.Identifier{databaseName}.Sanitize(),
		pgx.Identifier{databaseName}.Sanitize(), identifiers[0],
		pgx.Identifier{databaseName}.Sanitize(), identifiers[1], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2], identifiers[0], identifiers[1], identifiers[2], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2], identifiers[1], pgx.Identifier{databaseName}.Sanitize(),
	)); err != nil {
		t.Fatalf("provision generation-fence roles: %v", err)
	}
	t.Cleanup(func() {
		for _, identifier := range identifiers {
			_, _ = owner.Exec(context.Background(), "drop owned by "+identifier)
			_, _ = owner.Exec(context.Background(), "drop role if exists "+identifier)
		}
	})
	return generationFenceRoles{
		compilerURL: roleURL(parsed, compilerRole, password),
		readURL:     roleURL(parsed, readRole, password),
		writerURL:   roleURL(parsed, writerRole, password),
	}
}

func waitForApplyOrAdvisoryWait(t *testing.T, owner *database.Pool, applyDone <-chan error) (error, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-applyDone:
			return err, true
		default:
		}
		var waiters int
		if err := owner.QueryRow(context.Background(), `select count(*) from pg_stat_activity
			where datname=current_database() and wait_event_type='Lock' and wait_event='advisory'`).Scan(&waiters); err != nil {
			t.Fatal(err)
		}
		if waiters > 0 {
			return nil, false
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("schema update neither completed nor waited on the generation lock")
	return nil, false
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
