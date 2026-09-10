//go:build integration

package membership

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestResolveTraversalReadsTheRetiredBoundaryFromOneStatement(t *testing.T) {
	fixture := newTraversalFixture(t)
	fixture.enterDualWrite(t, 5)
	fixture.enterReady(t)
	fixture.enterRetired(t)
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
	t.Cleanup(func() { _ = conn.Close(fixture.ctx) })
	tx, err := conn.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(fixture.ctx) }()

	trace.start()
	traversal, err := ResolveTraversal(fixture.ctx, tx, fixture.collectionID)
	statements := trace.stop()
	if err != nil {
		t.Fatalf("ResolveTraversal(): %v", err)
	}
	if len(statements) != 1 {
		t.Fatalf("ResolveTraversal statement count = %d, want 1: %v", len(statements), statements)
	}
	statement := statements[0]
	for _, fragment := range []string{
		"pinned_schema as materialized",
		"from pgbase_meta.schema_state",
		"limit 1 for share",
		"pgbase_meta.system_migrations",
		"pgbase_meta.record_membership_capability",
		"pgbase_meta.collection_creation_counters",
		"pgbase_meta.record_creation_ordinals",
		"pgbase_meta.record_membership_backfills",
		"pgbase_meta.runtime_writer_leases",
	} {
		if !strings.Contains(statement, fragment) {
			t.Fatalf("single traversal statement is missing %q: %s", fragment, statement)
		}
	}
	for _, forbidden := range []string{"for share of capability", "for key share", "for update"} {
		if strings.Contains(statement, forbidden) {
			t.Fatalf("single traversal statement unexpectedly locks membership rows with %q: %s", forbidden, statement)
		}
	}
	if traversal.state != StateReady || traversal.Generation != 1 || traversal.HighWater != 5 ||
		!traversal.ready {
		t.Fatalf("retired traversal = %#v", traversal)
	}
	if traversal.TransactionTime.IsZero() || traversal.TransactionTime.Location() != time.UTC {
		t.Fatalf("transaction time = %#v, want non-zero UTC", traversal.TransactionTime)
	}
	var transactionTime time.Time
	if err := tx.QueryRow(fixture.ctx, `select transaction_timestamp()`).Scan(&transactionTime); err != nil {
		t.Fatal(err)
	}
	if !traversal.TransactionTime.Equal(transactionTime) {
		t.Fatalf("resolver transaction time = %s, PostgreSQL transaction time = %s",
			traversal.TransactionTime, transactionTime)
	}
}

// This binary can neither issue nor decode a v4 cursor, so every rollout state
// short of the retired terminal one is a database it cannot page at all. It
// fails those closed rather than serving a first page it could not continue.
func TestResolveTraversalAdmitsOnlyTheRetiredBoundary(t *testing.T) {
	fixture := newTraversalFixture(t)
	_, err := fixture.resolve(t)
	assertTraversalUnavailable(t, err)

	fixture.enterDualWrite(t, 5)
	_, err = fixture.resolve(t)
	assertTraversalUnavailable(t, err)

	fixture.enterReady(t)
	_, err = fixture.resolve(t)
	assertTraversalUnavailable(t, err)

	service := New(fixture.pool)
	runtime, err := RegisterWriter(fixture.ctx, service, WriterC)
	if err != nil {
		t.Fatal(err)
	}
	proof := FleetProof{Members: []FleetMember{runtime.FleetMember()}}
	if _, err := service.EnableV5(fixture.ctx, proof); err != nil {
		t.Fatal(err)
	}
	// v5 issuance alone is not enough: DisableV5 can still take it back to 4,
	// so the boundary is not yet one this binary can commit to.
	_, err = fixture.resolve(t)
	assertTraversalUnavailable(t, err)

	if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.record_membership_capability set
		cutover_at = clock_timestamp() - interval '4 minutes',
		ready_at = clock_timestamp() - interval '3 minutes',
		v5_enabled_at = clock_timestamp() - interval '2 minutes',
		v4_decode_until = clock_timestamp() - interval '1 minute',
		updated_at = clock_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetireV4(fixture.ctx, proof); err != nil {
		t.Fatal(err)
	}
	retired, err := fixture.resolve(t)
	if err != nil {
		t.Fatalf("retired traversal rejected: %v", err)
	}
	if !retired.ready || retired.Generation != 1 || retired.HighWater != 5 {
		t.Fatalf("retired traversal = %#v", retired)
	}
	if err := retired.ValidateV5(1, 5); err != nil {
		t.Fatalf("v5 continuation after v4 retirement: %v", err)
	}
	if _, err := service.DisableV5(fixture.ctx); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("DisableV5 after retirement = %v, want an invalid state", err)
	}
}

func TestResolveTraversalFailsClosedDuringCutover(t *testing.T) {
	fixture := newTraversalFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.record_membership_capability set
		state = 'cutover', generation = 1, minimum_writer_version = 1,
		cutover_at = clock_timestamp(), updated_at = clock_timestamp()
		where singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
		set generation = 1, updated_at = clock_timestamp() where collection_id = $1`,
		fixture.collectionID); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.resolve(t)
	assertTraversalUnavailable(t, err)
}

func TestResolveTraversalRejectsEveryPartialInstallationAsUnavailable(t *testing.T) {
	objects := []string{
		"record_membership_capability",
		"collection_creation_counters",
		"record_creation_ordinals",
		"record_membership_backfills",
		"runtime_writer_leases",
	}
	for _, object := range objects {
		t.Run(object, func(t *testing.T) {
			fixture := newTraversalFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx,
				`drop table pgbase_meta.`+pgx.Identifier{object}.Sanitize()+` cascade`); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.resolve(t)
			assertTraversalUnavailable(t, err)
		})
	}
}

func TestResolveTraversalRejectsMissingCapabilityRowAsUnavailable(t *testing.T) {
	fixture := newTraversalFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx,
		`delete from pgbase_meta.record_membership_capability where singleton`); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.resolve(t)
	assertTraversalUnavailable(t, err)
}

func TestResolveTraversalRejectsUnknownOrMalformedMigrationHistoryAsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "missing v7", sql: `delete from pgbase_meta.system_migrations where version = 7`},
		{name: "checksum mismatch", sql: `update pgbase_meta.system_migrations
			set checksum = decode(repeat('00', 32), 'hex') where version = 7`},
		{name: "history gap", sql: `delete from pgbase_meta.system_migrations where version = 6`},
		{name: "unknown future", sql: `insert into pgbase_meta.system_migrations
			(version, name, checksum, execution_ms)
			values (99, 'unknown_future', decode(repeat('99', 32), 'hex'), 0)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTraversalFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx, test.sql); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.resolve(t)
			assertTraversalUnavailable(t, err)
		})
	}
}

func TestResolveTraversalRejectsMalformedCapabilityMatrixAsUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		generation int64
	}{
		{
			name: "off with activation residue",
			capability: `update pgbase_meta.record_membership_capability
				set v5_enabled_at = updated_at where singleton`,
		},
		{
			name: "dual write without counts",
			capability: `update pgbase_meta.record_membership_capability set
				state = 'dual_write', generation = 1, minimum_writer_version = 1,
				cutover_at = updated_at where singleton`,
			generation: 1,
		},
		{
			name: "ready v5 without activation fields",
			capability: `update pgbase_meta.record_membership_capability set
				state = 'ready', generation = 1, minimum_writer_version = 2,
				cursor_issue_version = 5, cutover_at = updated_at,
				cutover_collection_count = 1, cutover_record_count = 0,
				ready_at = updated_at, ready_collection_count = 1 where singleton`,
			generation: 1,
		},
		{
			name: "ready v4 with activation residue",
			capability: `update pgbase_meta.record_membership_capability set
				state = 'ready', generation = 1, minimum_writer_version = 1,
				cutover_at = updated_at, cutover_collection_count = 1,
				cutover_record_count = 0, ready_at = updated_at,
				ready_collection_count = 1, v5_enabled_at = updated_at where singleton`,
			generation: 1,
		},
		{
			name: "inverted ready timeline",
			capability: `update pgbase_meta.record_membership_capability set
				state = 'ready', generation = 1, minimum_writer_version = 1,
				cutover_at = updated_at, cutover_collection_count = 1,
				cutover_record_count = 0, ready_at = updated_at - interval '1 second',
				ready_collection_count = 1 where singleton`,
			generation: 1,
		},
		{
			// Drift between cutover_collection_count and ready_collection_count
			// is deliberately NOT rejected: a collection created in the backfill
			// window is counted only by the later receipt. What must still be
			// rejected is a missing receipt, which no rollout transition writes.
			name: "ready without a collection receipt",
			capability: `update pgbase_meta.record_membership_capability set
				state = 'ready', generation = 1, minimum_writer_version = 1,
				cutover_at = updated_at, cutover_collection_count = 1,
				cutover_record_count = 0, ready_at = updated_at,
				ready_collection_count = null where singleton`,
			generation: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTraversalFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx, `alter table pgbase_meta.record_membership_capability
				drop constraint record_membership_capability_rollout_state_check,
				drop constraint record_membership_capability_timeline_check`); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.pool.Exec(fixture.ctx, test.capability); err != nil {
				t.Fatal(err)
			}
			if test.generation != 0 {
				if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
					set generation = $2 where collection_id = $1`, fixture.collectionID, test.generation); err != nil {
					t.Fatal(err)
				}
			}
			_, err := fixture.resolve(t)
			assertTraversalUnavailable(t, err)
		})
	}
}

func TestResolveTraversalRejectsMissingOrMalformedCollectionCounterAsUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, traversalFixture)
	}{
		{
			name: "missing counter",
			prepare: func(t *testing.T, fixture traversalFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `delete from pgbase_meta.collection_creation_counters
					where collection_id = $1`, fixture.collectionID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "generation mismatch",
			prepare: func(t *testing.T, fixture traversalFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
					set generation = 1 where collection_id = $1`, fixture.collectionID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "negative high water",
			prepare: func(t *testing.T, fixture traversalFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `alter table pgbase_meta.collection_creation_counters
					drop constraint collection_creation_counters_last_ordinal_check`); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
					set last_ordinal = -1 where collection_id = $1`, fixture.collectionID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "off state with allocated ordinal",
			prepare: func(t *testing.T, fixture traversalFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
					set last_ordinal = 1 where collection_id = $1`, fixture.collectionID); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTraversalFixture(t)
			test.prepare(t, fixture)
			_, err := fixture.resolve(t)
			assertTraversalUnavailable(t, err)
		})
	}
}

func TestResolveTraversalMapsDatabaseErrorsToUnavailable(t *testing.T) {
	fixture := newTraversalFixture(t)
	connection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	_, err = ResolveTraversal(canceled, tx, fixture.collectionID)
	assertTraversalUnavailable(t, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveTraversal() error = %v, want preserved database cause", err)
	}
}

func TestResolveTraversalPinsSchemaWithoutLockingCapabilityOrCounterRows(t *testing.T) {
	fixture := newTraversalFixture(t)
	fixture.enterDualWrite(t, 5)
	fixture.enterReady(t)
	fixture.enterRetired(t)
	connection, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	if _, err := ResolveTraversal(fixture.ctx, tx, fixture.collectionID); err != nil {
		t.Fatal(err)
	}

	for name, update := range map[string]func() error{
		"capability": func() error {
			_, updateErr := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.record_membership_capability
				set updated_at = clock_timestamp() where singleton`)
			return updateErr
		},
		"counter": func() error {
			_, updateErr := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.collection_creation_counters
				set updated_at = clock_timestamp() where collection_id = $1`, fixture.collectionID)
			return updateErr
		},
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				done <- update()
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s update blocked by traversal resolver", name)
			}
		})
	}

	schemaDone := make(chan error, 1)
	go func() {
		_, updateErr := fixture.pool.Exec(fixture.ctx, `update pgbase_meta.schema_state
			set updated_at = clock_timestamp() where singleton`)
		schemaDone <- updateErr
	}()
	select {
	case err := <-schemaDone:
		t.Fatalf("schema update bypassed traversal SHARE pin: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := tx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-schemaDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("schema update remained blocked after traversal transaction ended")
	}
}

func TestResolveTraversalFreshSchemaPlanStaysBelowJITThreshold(t *testing.T) {
	fixture := newTraversalFixture(t)
	versions, checksums, err := currentWriterMigrationHistory()
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`explain (format json) `+traversalResolutionSelect,
		fixture.collectionID,
		versions,
		checksums,
		len(versions)+1,
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var explained []struct {
		Plan struct {
			TotalCost float64 `json:"Total Cost"`
		} `json:"Plan"`
		JIT json.RawMessage `json:"JIT"`
	}
	if err := json.Unmarshal(raw, &explained); err != nil || len(explained) != 1 {
		t.Fatalf("decode traversal EXPLAIN: %v raw=%s", err, raw)
	}
	const postgresDefaultJITAboveCost = 100000
	if explained[0].Plan.TotalCost >= postgresDefaultJITAboveCost {
		t.Fatalf("fresh traversal plan total cost = %.2f, want below JIT threshold %d",
			explained[0].Plan.TotalCost, postgresDefaultJITAboveCost)
	}
	if len(explained[0].JIT) != 0 && string(explained[0].JIT) != "null" {
		t.Fatalf("fresh traversal plan unexpectedly enables JIT: %s", explained[0].JIT)
	}
	var serverVersion int
	if err := fixture.pool.QueryRow(fixture.ctx,
		`select current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if serverVersion < 160000 {
		t.Fatalf("PostgreSQL server version = %d, want supported version 16+", serverVersion)
	}
	t.Logf("PostgreSQL %d traversal plan total cost %.2f", serverVersion/10000, explained[0].Plan.TotalCost)
}

type traversalFixture struct {
	ctx          context.Context
	databaseURL  string
	pool         *database.Pool
	collectionID uuid.UUID
	createdAt    time.Time
}

func newTraversalFixture(t *testing.T) traversalFixture {
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
	collectionID := uuid.MustParse("0199b600-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'traversal_records', 'traversal_records', 'active', $2, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	return traversalFixture{
		ctx: ctx, databaseURL: databaseURL, pool: pool,
		collectionID: collectionID, createdAt: createdAt,
	}
}

func (f traversalFixture) resolve(t *testing.T) (Traversal, error) {
	t.Helper()
	var traversal Traversal
	err := f.pool.InTx(f.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		traversal, err = ResolveTraversal(f.ctx, tx, f.collectionID)
		return err
	})
	return traversal, err
}

func (f traversalFixture) enterDualWrite(t *testing.T, highWater int64) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `update pgbase_meta.record_membership_capability set
		state = 'dual_write', generation = 1, minimum_writer_version = 1,
		cutover_at = clock_timestamp(), cutover_collection_count = 1,
		cutover_record_count = $1, updated_at = clock_timestamp()
		where singleton`, highWater); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `update pgbase_meta.collection_creation_counters
		set generation = 1, last_ordinal = $2, updated_at = clock_timestamp()
		where collection_id = $1`, f.collectionID, highWater); err != nil {
		t.Fatal(err)
	}
}

func (f traversalFixture) enterReady(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `update pgbase_meta.record_membership_capability set
		state = 'ready', ready_at = clock_timestamp(), ready_collection_count = 1,
		updated_at = clock_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}
}

func (f traversalFixture) enterRetired(t *testing.T) {
	t.Helper()
	testutil.RetireRecordMembershipCapability(t, f.ctx, f.pool)
}

func assertTraversalUnavailable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTraversalInvalid) {
		t.Fatalf("ResolveTraversal() error = %v, want membership unavailable", err)
	}
	if err.Error() != ErrUnavailable.Error() {
		t.Fatalf("ResolveTraversal() error text = %q, want opaque %q", err, ErrUnavailable)
	}
}

// cutover_collection_count and ready_collection_count are rollout receipts
// stamped at two different moments, and the product deliberately allows a
// collection to be created in between: schema.Store.insertMembershipMetadata
// seeds a ready backfill row whenever the capability is dual_write or ready.
// Treating the two counts as equal therefore rejected a state the database
// accepts and PromoteReady produces, which took every list on every collection
// down permanently with no transition able to repair it.
func TestResolveTraversalAcceptsCollectionAddedDuringBackfillWindow(t *testing.T) {
	recordID := uuid.MustParse("0199b400-0000-7000-8000-0000000000d1")
	fixture := newMembershipConcurrencyFixture(t, "membership_window_add", []uuid.UUID{recordID})
	fixture.cutover(t)

	addedID := uuid.MustParse("0199b400-0000-7000-8000-0000000000d2")
	insertPostTransitionMembershipCollection(t, fixture, addedID, "membership_window_added", true)

	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Done {
		t.Fatalf("backfill = %#v, want done", progress)
	}
	if _, err := fixture.service.PromoteReady(fixture.ctx); err != nil {
		t.Fatal(err)
	}

	var cutoverCount, readyCount int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select cutover_collection_count,
		ready_collection_count from pgbase_meta.record_membership_capability
		where singleton`).Scan(&cutoverCount, &readyCount); err != nil {
		t.Fatal(err)
	}
	if cutoverCount == readyCount {
		t.Fatalf("rollout receipts = cutover %d ready %d, want the fixture to reproduce the drift",
			cutoverCount, readyCount)
	}

	testutil.RetireRecordMembershipCapability(t, fixture.ctx, fixture.pool)
	for _, collectionID := range []uuid.UUID{fixture.collectionID, addedID} {
		var traversal Traversal
		if err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			traversal, err = ResolveTraversal(fixture.ctx, tx, collectionID)
			return err
		}); err != nil {
			t.Fatalf("ResolveTraversal(%s) error = %v, want a usable ready traversal", collectionID, err)
		}
		if !traversal.ready {
			t.Fatalf("ResolveTraversal(%s) traversal = %#v, want the retired boundary",
				collectionID, traversal)
		}
	}
}
