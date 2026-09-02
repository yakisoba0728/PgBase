//go:build integration

package membership

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

type bootstrapFixture struct {
	ctx          context.Context
	databaseURL  string
	pool         *database.Pool
	service      *Service
	collectionID uuid.UUID
	tableName    string
}

// newBootstrapFixture builds what a brand new install looks like the moment an
// administrator has created one collection: a migrated database whose
// capability is still the row migration 7 seeded, and a collection with no
// record in it.
func newBootstrapFixture(t *testing.T) bootstrapFixture {
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
	collectionID := uuid.MustParse("0199b700-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'bootstrap_records', 'bootstrap_records', 'active', $2, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table app.bootstrap_records (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}
	return bootstrapFixture{
		ctx: ctx, databaseURL: databaseURL, pool: pool, service: New(pool),
		collectionID: collectionID, tableName: "bootstrap_records",
	}
}

func (f bootstrapFixture) insertRecord(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `insert into app.bootstrap_records (id, created_at, updated_at)
		values ($1, transaction_timestamp(), transaction_timestamp())`, id); err != nil {
		t.Fatal(err)
	}
}

func (f bootstrapFixture) status(t *testing.T) Status {
	t.Helper()
	status, err := f.service.Status(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// A retired database is the only shape ResolveTraversal admits, so the
// bootstrap has to leave a row every one of those clauses accepts rather than
// merely one the CHECK constraints tolerate.
func (f bootstrapFixture) requireRetiredTerminalState(t *testing.T) {
	t.Helper()
	status := f.status(t)
	if status.State != StateReady || status.Generation != 1 ||
		status.MinimumWriterVersion != WriterB || status.CursorIssueVersion != 5 ||
		status.V4RetiredAt == nil || status.CutoverAt == nil || status.ReadyAt == nil ||
		status.V5EnabledAt == nil || status.DecodeV4Until == nil {
		t.Fatalf("bootstrapped capability = %#v, want the retired terminal state", status)
	}
	if !validTraversalStatus(status) {
		t.Fatalf("bootstrapped capability = %#v is not a status the resolver admits", status)
	}
	var counterGeneration, lastOrdinal int64
	if err := f.pool.QueryRow(f.ctx, `select generation, last_ordinal
		from pgbase_meta.collection_creation_counters where collection_id = $1`,
		f.collectionID).Scan(&counterGeneration, &lastOrdinal); err != nil {
		t.Fatal(err)
	}
	if counterGeneration != status.Generation || lastOrdinal != 0 {
		t.Fatalf("bootstrapped counter generation = %d last ordinal = %d, want %d and 0",
			counterGeneration, lastOrdinal, status.Generation)
	}
	var backfillState string
	var backfillGeneration, validatedBase, validatedSidecar int64
	if err := f.pool.QueryRow(f.ctx, `select state, generation,
		validated_base_count, validated_sidecar_count
		from pgbase_meta.record_membership_backfills where collection_id = $1`,
		f.collectionID).Scan(&backfillState, &backfillGeneration, &validatedBase, &validatedSidecar); err != nil {
		t.Fatal(err)
	}
	if backfillState != "ready" || backfillGeneration != status.Generation ||
		validatedBase != 0 || validatedSidecar != 0 {
		t.Fatalf("bootstrapped backfill = %s/%d validated %d/%d, want a ready generation %d row",
			backfillState, backfillGeneration, validatedBase, validatedSidecar, status.Generation)
	}
}

// A fresh install is normally started by several replicas at once. Exactly one
// of them may stamp the rollout receipts; the rest have to observe the work as
// already done rather than repeat it or fail.
func TestBootstrapFreshInstallPromotesConcurrentStartersExactlyOnce(t *testing.T) {
	fixture := newBootstrapFixture(t)
	const starters = 8
	promoted := make([]bool, starters)
	failures := make([]error, starters)
	var waiting sync.WaitGroup
	start := make(chan struct{})
	for index := range starters {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			// Each replica opens its own pool, so the promotions race across
			// connections rather than inside one.
			pool, err := database.Open(fixture.ctx, fixture.databaseURL, 5*time.Second)
			if err != nil {
				failures[index] = err
				return
			}
			defer pool.Close()
			<-start
			promoted[index], failures[index] = New(pool).BootstrapFreshInstall(fixture.ctx)
		}()
	}
	close(start)
	waiting.Wait()

	winners := 0
	for index := range starters {
		if failures[index] != nil {
			t.Fatalf("concurrent bootstrap %d: %v", index, failures[index])
		}
		if promoted[index] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent bootstrap promotions = %d, want exactly 1", winners)
	}
	fixture.requireRetiredTerminalState(t)
}

// The whole point of the precondition: a database that carries data has a
// backfill to run, and no shortcut may stand in for it.
func TestBootstrapFreshInstallRefusesRecordsAndLeavesTheRealRollout(t *testing.T) {
	fixture := newBootstrapFixture(t)
	fixture.insertRecord(t, uuid.MustParse("0199b700-0000-7000-8000-0000000000a1"))

	promoted, err := fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err != nil {
		t.Fatalf("BootstrapFreshInstall on a populated database: %v", err)
	}
	if promoted {
		t.Fatal("BootstrapFreshInstall promoted a database that already holds a record")
	}
	if status := fixture.status(t); status.State != StateOff || status.Generation != 0 ||
		status.CursorIssueVersion != 4 || status.MinimumWriterVersion != WriterA0 {
		t.Fatalf("refused capability = %#v, want the seeded off row", status)
	}

	// The real rollout still owns this database, all the way to retirement.
	runtime, err := RegisterWriter(fixture.ctx, fixture.service, WriterC)
	if err != nil {
		t.Fatal(err)
	}
	proof := FleetProof{Members: []FleetMember{runtime.FleetMember()}}
	if _, err := fixture.service.Cutover(fixture.ctx, proof); err != nil {
		t.Fatalf("Cutover after a refused bootstrap: %v", err)
	}
	progress, err := fixture.service.Backfill(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Done {
		t.Fatalf("backfill after a refused bootstrap = %#v, want done", progress)
	}
	if _, err := fixture.service.PromoteReady(fixture.ctx); err != nil {
		t.Fatalf("PromoteReady after a refused bootstrap: %v", err)
	}
	if _, err := fixture.service.EnableV5(fixture.ctx, proof); err != nil {
		t.Fatalf("EnableV5 after a refused bootstrap: %v", err)
	}
}

func TestBootstrapFreshInstallIsANoOpOnARetiredDatabase(t *testing.T) {
	fixture := newBootstrapFixture(t)
	promoted, err := fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err != nil || !promoted {
		t.Fatalf("first BootstrapFreshInstall = %t, %v", promoted, err)
	}
	first := fixture.status(t)

	promoted, err = fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err != nil {
		t.Fatalf("second BootstrapFreshInstall: %v", err)
	}
	if promoted {
		t.Fatal("second BootstrapFreshInstall promoted an already retired database")
	}
	second := fixture.status(t)
	if !second.UpdatedAt.Equal(first.UpdatedAt) || !second.V4RetiredAt.Equal(*first.V4RetiredAt) {
		t.Fatalf("second BootstrapFreshInstall rewrote the capability: %#v then %#v", first, second)
	}
}

// The capability's state machine lives in migration 7, not in this package. A
// bootstrap whose row the database refuses has to abort the whole promotion
// rather than report a success nothing durable backs, so the write is driven
// through a trigger that mangles exactly one column of it.
func TestBootstrapFreshInstallFailsLoudlyOnAnInconsistentCapabilityRow(t *testing.T) {
	fixture := newBootstrapFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `create function
		pgbase_meta.drop_bootstrap_activation() returns trigger language plpgsql as $$
		begin
			new.v5_enabled_at := null;
			return new;
		end
		$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `create trigger drop_bootstrap_activation
		before update on pgbase_meta.record_membership_capability
		for each row execute function pgbase_meta.drop_bootstrap_activation()`); err != nil {
		t.Fatal(err)
	}

	promoted, err := fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err == nil {
		t.Fatalf("BootstrapFreshInstall = %t with no error, want the CHECK violation surfaced", promoted)
	}
	if promoted {
		t.Fatal("BootstrapFreshInstall reported a promotion the database rejected")
	}
	if status := fixture.status(t); status.State != StateOff || status.Generation != 0 {
		t.Fatalf("capability after a rejected bootstrap = %#v, want the seeded off row", status)
	}
	var counterGeneration int64
	if err := fixture.pool.QueryRow(fixture.ctx, `select generation
		from pgbase_meta.collection_creation_counters where collection_id = $1`,
		fixture.collectionID).Scan(&counterGeneration); err != nil {
		t.Fatal(err)
	}
	if counterGeneration != 0 {
		t.Fatalf("counter generation after a rejected bootstrap = %d, want 0", counterGeneration)
	}
}

// Registration is what triggers the bootstrap, and only the current release
// may trigger it: the terminal state raises the writer floor to Release B, so
// an artifact that registered as an earlier release would lock itself out.
func TestRegisterWriterBootstrapsOnlyTheCurrentRelease(t *testing.T) {
	for _, release := range []struct {
		name    string
		version WriterVersion
	}{
		{name: "release A0", version: WriterA0},
		{name: "release A", version: WriterA},
		{name: "release B", version: WriterB},
	} {
		t.Run(release.name, func(t *testing.T) {
			fixture := newBootstrapFixture(t)
			if _, err := RegisterWriter(fixture.ctx, fixture.service, release.version); err != nil {
				t.Fatal(err)
			}
			if status := fixture.status(t); status.State != StateOff || status.Generation != 0 {
				t.Fatalf("capability after registering %s = %#v, want the seeded off row",
					release.name, status)
			}
		})
	}
	t.Run("release C", func(t *testing.T) {
		fixture := newBootstrapFixture(t)
		if _, err := RegisterWriter(fixture.ctx, fixture.service, WriterC); err != nil {
			t.Fatal(err)
		}
		fixture.requireRetiredTerminalState(t)
	})
}

// A mixed fleet is mid-rollout by definition. Promoting under a live older
// writer would raise the minimum writer version out from under it, so the
// bootstrap holds off for exactly as long as retirement itself would.
func TestBootstrapFreshInstallWaitsForAFleetThatIsStillOnAnOlderRelease(t *testing.T) {
	fixture := newBootstrapFixture(t)
	if _, err := RegisterWriter(fixture.ctx, fixture.service, WriterA); err != nil {
		t.Fatal(err)
	}
	promoted, err := fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err != nil {
		t.Fatalf("BootstrapFreshInstall under a Release A lease: %v", err)
	}
	if promoted {
		t.Fatal("BootstrapFreshInstall promoted a database with a live Release A writer")
	}
	if status := fixture.status(t); status.State != StateOff {
		t.Fatalf("capability = %#v, want the seeded off row", status)
	}

	if _, err := fixture.pool.Exec(fixture.ctx,
		`delete from pgbase_meta.runtime_writer_leases`); err != nil {
		t.Fatal(err)
	}
	promoted, err = fixture.service.BootstrapFreshInstall(fixture.ctx)
	if err != nil || !promoted {
		t.Fatalf("BootstrapFreshInstall after the older writer drained = %t, %v", promoted, err)
	}
	fixture.requireRetiredTerminalState(t)
}

// Rollout history is not always visible in the capability row alone: a
// database that reached dual-write and was rolled back still carries counters
// and sidecars the shortcut would strand.
func TestBootstrapFreshInstallRefusesLeftoverRolloutHistory(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, fixture bootstrapFixture)
	}{
		{
			name: "counter ordinal already advanced",
			arrange: func(t *testing.T, fixture bootstrapFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx,
					`update pgbase_meta.collection_creation_counters
					set last_ordinal = 1, updated_at = transaction_timestamp()`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// Cutover moves the counter and the backfill row together, and the
			// deferred foreign key between them is why this arrives as one
			// transaction rather than two statements.
			name: "cutover already started",
			arrange: func(t *testing.T, fixture bootstrapFixture) {
				if err := fixture.pool.InTx(fixture.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
					if _, err := tx.Exec(fixture.ctx,
						`update pgbase_meta.collection_creation_counters
						set generation = 1, updated_at = transaction_timestamp()`); err != nil {
						return err
					}
					_, err := tx.Exec(fixture.ctx,
						`update pgbase_meta.record_membership_backfills
						set generation = 1, state = 'backfilling', cutover_count = 0,
						updated_at = transaction_timestamp()`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "orphaned creation ordinal",
			arrange: func(t *testing.T, fixture bootstrapFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx,
					`insert into pgbase_meta.record_creation_ordinals
					(collection_id, record_id, ordinal) values ($1, $2, 1)`,
					fixture.collectionID,
					uuid.MustParse("0199b700-0000-7000-8000-0000000000b1")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "collection without its backfill row",
			arrange: func(t *testing.T, fixture bootstrapFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx,
					`delete from pgbase_meta.record_membership_backfills`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "catalog row without its table",
			arrange: func(t *testing.T, fixture bootstrapFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx,
					`drop table app.bootstrap_records`); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBootstrapFixture(t)
			test.arrange(t, fixture)
			promoted, err := fixture.service.BootstrapFreshInstall(fixture.ctx)
			if err != nil {
				t.Fatalf("BootstrapFreshInstall: %v", err)
			}
			if promoted {
				t.Fatal("BootstrapFreshInstall promoted a database with rollout history")
			}
			if status := fixture.status(t); status.State != StateOff || status.Generation != 0 {
				t.Fatalf("capability = %#v, want the seeded off row", status)
			}
		})
	}
}

// A pre-membership database has no capability to promote, and the bootstrap
// must say so rather than treat a missing table as an empty rollout.
func TestBootstrapFreshInstallRequiresTheCompleteInstallation(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:6])
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	promoted, err := New(pool).BootstrapFreshInstall(ctx)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("BootstrapFreshInstall on a pre-membership database = %t, %v", promoted, err)
	}
}
