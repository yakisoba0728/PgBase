//go:build integration

package membership

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

type writerValidationTrace struct {
	mu         sync.Mutex
	enabled    bool
	statements []string
}

func (t *writerValidationTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.statements = append(t.statements, strings.Join(strings.Fields(strings.ToLower(data.SQL)), " "))
	}
	return ctx
}

func (*writerValidationTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *writerValidationTrace) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements = nil
	t.enabled = true
}

func (t *writerValidationTrace) stop() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
	return append([]string(nil), t.statements...)
}

func TestWriterValidationStatementCountsStayBoundedAcrossRolloutStates(t *testing.T) {
	fixture := newWriterValidationFixture(t)
	trace := &writerValidationTrace{}
	config, err := pgx.ParseConfig(fixture.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	conn, err := pgx.ConnectConfig(fixture.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(fixture.ctx); err != nil {
			t.Errorf("close writer validation trace connection: %v", err)
		}
	})

	states := []struct {
		name    string
		prepare func(*testing.T)
	}{
		{name: "off", prepare: func(*testing.T) {}},
		{name: "dual", prepare: fixture.enterDualWrite},
		{name: "ready", prepare: fixture.enterReady},
	}
	for _, state := range states {
		state.prepare(t)
		for _, version := range []WriterVersion{WriterA, WriterB, WriterC} {
			writer := fixture.writers[version]
			name := fmt.Sprintf("%s/writer_%d", state.name, version)
			t.Run(name+"/begin", func(t *testing.T) {
				statements := traceWriterBegin(t, fixture.ctx, conn, trace, writer)
				assertWriterValidationTrace(t, statements, false)
			})
			t.Run(name+"/lock_create", func(t *testing.T) {
				statements := traceWriterLockCreate(
					t,
					fixture.ctx,
					conn,
					trace,
					writer,
					fixture.collectionID,
				)
				assertWriterValidationTrace(t, statements, true)
			})
		}
	}
}

func TestWriterValidationRejectsEveryPartialInstallation(t *testing.T) {
	objects := []string{
		"record_membership_capability",
		"collection_creation_counters",
		"record_creation_ordinals",
		"record_membership_backfills",
		"runtime_writer_leases",
	}
	for _, object := range objects {
		t.Run(object, func(t *testing.T) {
			fixture := newWriterValidationFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx,
				`drop table pgbase_meta.`+pgx.Identifier{object}.Sanitize()+` cascade`); err != nil {
				t.Fatal(err)
			}
			writer := fixture.writers[WriterA]
			assertWriterValidationRejected(t, fixture, func(tx pgx.Tx) error {
				_, err := writer.Begin(fixture.ctx, tx)
				return err
			})
			assertWriterValidationRejected(t, fixture, func(tx pgx.Tx) error {
				_, err := writer.LockCreate(fixture.ctx, tx, fixture.collectionID)
				return err
			})
		})
	}
}

func TestWriterValidationRejectsIncompleteOrUnknownMigrationHistory(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "checksum_mismatch", sql: `update pgbase_meta.system_migrations
			set checksum = decode(repeat('00', 32), 'hex') where version = 7`},
		{name: "gap", sql: `delete from pgbase_meta.system_migrations where version = 6`},
		{name: "unknown_future", sql: `insert into pgbase_meta.system_migrations
			(version, name, checksum, execution_ms)
			values (99, 'unknown_future', decode(repeat('00', 32), 'hex'), 0)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWriterValidationFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx, test.sql); err != nil {
				t.Fatal(err)
			}
			writer := fixture.writers[WriterA]
			assertWriterValidationRejected(t, fixture, func(tx pgx.Tx) error {
				_, err := writer.Begin(fixture.ctx, tx)
				return err
			})
			assertWriterValidationRejected(t, fixture, func(tx pgx.Tx) error {
				_, err := writer.LockCreate(fixture.ctx, tx, fixture.collectionID)
				return err
			})
		})
	}
}

func TestWriterValidationRejectsRevokedLease(t *testing.T) {
	fixture := newWriterValidationFixture(t)
	runtime := fixture.runtimes[WriterA]
	if tag, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.runtime_writer_leases
		where instance_id = $1`, runtime.instanceID); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("revoked writer leases = %d, want 1", tag.RowsAffected())
	}
	writer := fixture.writers[WriterA]
	assertWriterLeaseRejected := func(t *testing.T, validate func(pgx.Tx) error) {
		t.Helper()
		err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, validate)
		if !errors.Is(err, ErrWriterTooOld) {
			t.Fatalf("revoked writer validation error = %v, want ErrWriterTooOld", err)
		}
	}
	assertWriterLeaseRejected(t, func(tx pgx.Tx) error {
		_, err := writer.Begin(fixture.ctx, tx)
		return err
	})
	assertWriterLeaseRejected(t, func(tx pgx.Tx) error {
		_, err := writer.LockCreate(fixture.ctx, tx, fixture.collectionID)
		return err
	})
}

func TestWriterValidationRejectsLeaseRevokedWhileCapabilityLockIsDelayed(t *testing.T) {
	fixture := newWriterValidationFixture(t)
	blocker, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	blockerTx, err := blocker.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(blockerTx)
	var singleton bool
	if err := blockerTx.QueryRow(fixture.ctx, `select singleton
		from pgbase_meta.record_membership_capability where singleton for update`).Scan(&singleton); err != nil {
		t.Fatal(err)
	}

	writerConnection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writerConnection.Release()
	writerTx, err := writerConnection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(writerTx)
	writerPID := membershipConcurrencyBackendPID(t, writerTx)
	validationCtx, cancelValidation := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer cancelValidation()
	result := make(chan error, 1)
	go func() {
		_, err := fixture.writers[WriterA].Begin(validationCtx, writerTx)
		result <- err
	}()
	waitForMembershipPostgresLock(
		t,
		fixture.pool,
		writerPID,
		"with locked_capability as materialized",
	)

	runtime := fixture.runtimes[WriterA]
	if tag, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.runtime_writer_leases
		where instance_id = $1`, runtime.instanceID); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("revoked delayed writer leases = %d, want 1", tag.RowsAffected())
	}
	if err := blockerTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if err := awaitMembershipConcurrencyError(t, result); !errors.Is(err, ErrWriterTooOld) {
		t.Fatalf("delayed revoked writer validation error = %v, want ErrWriterTooOld", err)
	}
}

func TestWriterValidationLeaseLockAllowsHeartbeatAndSerializesRevocation(t *testing.T) {
	fixture := newWriterValidationFixture(t)
	connection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	writerTx, err := connection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackMembershipConcurrencyTx(writerTx)
	if _, err := fixture.writers[WriterA].Begin(fixture.ctx, writerTx); err != nil {
		t.Fatal(err)
	}

	heartbeatDone := make(chan error, 1)
	go func() { heartbeatDone <- fixture.runtimes[WriterA].Ready(fixture.ctx) }()
	select {
	case err := <-heartbeatDone:
		if err != nil {
			t.Fatalf("heartbeat while writer holds lease key-share lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer lease validation blocked its own heartbeat")
	}

	revocationDone := make(chan error, 1)
	instanceID := fixture.runtimes[WriterA].instanceID
	go func() {
		_, deleteErr := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.runtime_writer_leases
			where instance_id = $1`, instanceID)
		revocationDone <- deleteErr
	}()
	select {
	case err := <-revocationDone:
		t.Fatalf("lease revocation bypassed the writer lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := writerTx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-revocationDone:
		if err != nil {
			t.Fatalf("lease revocation after writer completion: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lease revocation remained blocked after writer completion")
	}
}

func assertWriterValidationRejected(
	t *testing.T,
	fixture writerValidationFixture,
	validate func(pgx.Tx) error,
) {
	t.Helper()
	err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, validate)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("writer validation error = %v, want ErrInvalidState", err)
	}
}

func traceWriterBegin(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	trace *writerValidationTrace,
	writer Writer,
) []string {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	trace.start()
	_, err = writer.Begin(ctx, tx)
	statements := trace.stop()
	if err != nil {
		t.Fatalf("Writer.Begin(): %v\n%s", err, formatWriterValidationTrace(statements))
	}
	return statements
}

func traceWriterLockCreate(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	trace *writerValidationTrace,
	writer Writer,
	collectionID uuid.UUID,
) []string {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	trace.start()
	_, err = writer.LockCreate(ctx, tx, collectionID)
	statements := trace.stop()
	if err != nil {
		t.Fatalf("Writer.LockCreate(): %v\n%s", err, formatWriterValidationTrace(statements))
	}
	return statements
}

func assertWriterValidationTrace(t *testing.T, statements []string, create bool) {
	t.Helper()
	want := 2
	if create {
		want = 1
	}
	if len(statements) != want {
		t.Fatalf("writer validation statement count = %d, want %d\n%s",
			len(statements), want, formatWriterValidationTrace(statements))
	}
	validationIndex := 1
	if create {
		validationIndex = 0
		statement := statements[0]
		schemaIndex := strings.Index(statement, "from pgbase_meta.schema_state")
		counterIndex := strings.Index(statement, "join pgbase_meta.collection_creation_counters")
		capabilityIndex := strings.Index(statement, "join pgbase_meta.record_membership_capability")
		if schemaIndex < 0 || counterIndex <= schemaIndex || capabilityIndex <= counterIndex {
			t.Fatalf("single create statement does not preserve schema -> counter -> capability locks: %s", statement)
		}
		for _, fragment := range []string{
			"pinned_schema as materialized",
			"locked_counter as materialized",
			"locked_capability as materialized",
			"where singleton limit 1 for share",
			"for key share of creation_counter",
			"on capability.singleton limit 1 for share of capability",
		} {
			if !strings.Contains(statement, fragment) {
				t.Fatalf("single create statement is missing %q: %s", fragment, statement)
			}
		}
	} else if !strings.Contains(statements[0], "from pgbase_meta.schema_state") ||
		!strings.HasSuffix(statements[0], "for share") {
		t.Fatalf("first writer statement does not pin schema: %s", statements[0])
	}
	validation := statements[validationIndex]
	for _, fragment := range []string{
		"pgbase_meta.system_migrations",
		"pgbase_meta.record_membership_capability",
		"pgbase_meta.runtime_writer_leases",
		"for share",
		"for key share",
	} {
		if !strings.Contains(validation, fragment) {
			t.Fatalf("coalesced writer validation is missing %q: %s", fragment, validation)
		}
	}
}

func formatWriterValidationTrace(statements []string) string {
	if len(statements) == 0 {
		return "(no traced statements)"
	}
	lines := make([]string, len(statements))
	for index, statement := range statements {
		lines[index] = fmt.Sprintf("%d. %s", index+1, statement)
	}
	return strings.Join(lines, "\n")
}

type writerValidationFixture struct {
	ctx          context.Context
	databaseURL  string
	pool         *database.Pool
	service      *Service
	runtimes     map[WriterVersion]*Runtime
	writers      map[WriterVersion]Writer
	collectionID uuid.UUID
}

func newWriterValidationFixture(t *testing.T) writerValidationFixture {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID := uuid.MustParse("0199b500-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'writer_validation', 'writer_validation', 'active', $2, $2)`,
		collectionID,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
		collectionID,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table app.writer_validation (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}

	service := New(pool)
	runtimes := make(map[WriterVersion]*Runtime, 3)
	writers := make(map[WriterVersion]Writer, 3)
	for _, version := range []WriterVersion{WriterA, WriterB, WriterC} {
		runtime, err := RegisterWriter(ctx, service, version)
		if err != nil {
			t.Fatal(err)
		}
		writer, err := runtime.Writer()
		if err != nil {
			t.Fatal(err)
		}
		runtimes[version] = runtime
		writers[version] = writer
	}
	return writerValidationFixture{
		ctx:          ctx,
		databaseURL:  databaseURL,
		pool:         pool,
		service:      service,
		runtimes:     runtimes,
		writers:      writers,
		collectionID: collectionID,
	}
}

func (f writerValidationFixture) fleetProof() FleetProof {
	members := make([]FleetMember, 0, len(f.runtimes))
	for _, runtime := range f.runtimes {
		members = append(members, runtime.FleetMember())
	}
	sort.Slice(members, func(i, j int) bool {
		return bytes.Compare(members[i].InstanceID[:], members[j].InstanceID[:]) < 0
	})
	return FleetProof{Members: members}
}

func (f writerValidationFixture) enterDualWrite(t *testing.T) {
	t.Helper()
	if _, err := f.service.Cutover(f.ctx, f.fleetProof()); err != nil {
		t.Fatal(err)
	}
}

func (f writerValidationFixture) enterReady(t *testing.T) {
	t.Helper()
	progress, err := f.service.Backfill(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Ready || !progress.Done {
		t.Fatalf("empty writer validation backfill = %#v, want ready", progress)
	}
	if _, err := f.service.PromoteReady(f.ctx); err != nil {
		t.Fatal(err)
	}
}
