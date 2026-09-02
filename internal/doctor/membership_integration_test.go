//go:build integration

package doctor

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestRunReportsOneRedactedOrphanedMembershipSidecarFinding(t *testing.T) {
	for _, state := range []string{"dual_write", "ready"} {
		t.Run(state, func(t *testing.T) {
			fixture := newMembershipDoctorFixture(t, state)
			orphanIDs := []uuid.UUID{
				uuid.MustParse("0199c000-0000-7000-8000-000000000001"),
				uuid.MustParse("0199c000-0000-7000-8000-000000000002"),
			}
			if _, err := fixture.pool.Exec(context.Background(), `update pgbase_meta.collection_creation_counters
				set last_ordinal = 2, updated_at = $2 where collection_id = $1`,
				fixture.collectionID, fixture.createdAt); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.pool.Exec(context.Background(), `insert into pgbase_meta.record_creation_ordinals
				(collection_id, record_id, ordinal) values ($1, $2, 1), ($1, $3, 2)`,
				fixture.collectionID, orphanIDs[0], orphanIDs[1]); err != nil {
				t.Fatal(err)
			}

			report := runDoctor(t, fixture.pool)
			if report.OK || len(report.Findings) != 1 {
				t.Fatalf("report = %#v, want one blocking finding", report)
			}
			if got := report.Findings[0]; got != finding(
				"record_membership_sidecar_orphaned",
				"A record membership sidecar is missing its base record",
			) {
				t.Fatalf("finding = %#v", got)
			}
			assertBoundedMembershipReport(t, report, append(orphanIDs, fixture.collectionID)...)
		})
	}
}

func TestRunReportsMissingMembershipSidecarsOnlyWhenReady(t *testing.T) {
	for _, testCase := range []struct {
		state       string
		wantFinding bool
	}{
		{state: "off"},
		{state: "dual_write"},
		{state: "ready", wantFinding: true},
	} {
		t.Run(testCase.state, func(t *testing.T) {
			fixture := newMembershipDoctorFixture(t, testCase.state)
			baseIDs := []uuid.UUID{
				uuid.MustParse("0199c000-0000-7000-8000-000000000011"),
				uuid.MustParse("0199c000-0000-7000-8000-000000000012"),
			}
			if _, err := fixture.pool.Exec(context.Background(), `insert into app."order"
				(id, created_at, updated_at) values ($1, $3, $3), ($2, $3, $3)`,
				baseIDs[0], baseIDs[1], fixture.createdAt); err != nil {
				t.Fatal(err)
			}

			report := runDoctor(t, fixture.pool)
			if !testCase.wantFinding {
				if !report.OK || len(report.Findings) != 0 {
					t.Fatalf("report = %#v, want expected transitional gap to be healthy", report)
				}
				return
			}
			if report.OK || len(report.Findings) != 1 {
				t.Fatalf("report = %#v, want one blocking finding", report)
			}
			if got := report.Findings[0]; got != finding(
				"record_membership_sidecar_missing",
				"A base record is missing its required membership sidecar",
			) {
				t.Fatalf("finding = %#v", got)
			}
			assertBoundedMembershipReport(t, report, append(baseIDs, fixture.collectionID)...)
		})
	}
}

func TestRunFailsClosedOnPartialMembershipInstallation(t *testing.T) {
	t.Run("v6 history with a v7 fragment", func(t *testing.T) {
		databaseURL := testutil.NewDatabase(t)
		migrations := system.Migrations()
		if len(migrations) != 7 {
			t.Fatalf("membership manifest = %#v", migrations)
		}
		testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:6])
		pool, err := database.Open(context.Background(), databaseURL, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if _, err := pool.Exec(context.Background(), `create table pgbase_meta.runtime_writer_leases (
			partial_installation boolean
		)`); err != nil {
			t.Fatal(err)
		}
		assertInvalidMembershipInstallation(t, runDoctor(t, pool))
	})

	for _, object := range []string{
		"record_membership_capability",
		"collection_creation_counters",
		"record_creation_ordinals",
		"record_membership_backfills",
		"runtime_writer_leases",
	} {
		t.Run("v7 missing "+object, func(t *testing.T) {
			pool := newDoctorPool(t)
			identifier := pgx.Identifier{"pgbase_meta", object}.Sanitize()
			if _, err := pool.Exec(context.Background(), "drop table "+identifier+" cascade"); err != nil {
				t.Fatal(err)
			}
			assertInvalidMembershipInstallation(t, runDoctor(t, pool))
		})
	}

	for _, testCase := range []struct {
		name     string
		breakSQL string
	}{
		{
			name:     "v7 missing capability singleton",
			breakSQL: `delete from pgbase_meta.record_membership_capability where singleton`,
		},
		{
			name:     "v7 missing capability state column",
			breakSQL: `alter table pgbase_meta.record_membership_capability drop column state cascade`,
		},
		{
			name:     "v7 missing ordinal record ID column while off",
			breakSQL: `alter table pgbase_meta.record_creation_ordinals drop column record_id cascade`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			if _, err := pool.Exec(context.Background(), testCase.breakSQL); err != nil {
				t.Fatal(err)
			}
			assertInvalidMembershipInstallation(t, runDoctor(t, pool))
		})
	}
}

func TestRunFailsClosedOnMalformedMembershipCatalog(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		breakSQL string
	}{
		{
			name:     "missing lease expiry index",
			breakSQL: `drop index pgbase_meta.runtime_writer_leases_expiry_idx`,
		},
		{
			name:     "nullable lease start",
			breakSQL: `alter table pgbase_meta.runtime_writer_leases alter column started_at drop not null`,
		},
		{
			name:     "missing capability primary key",
			breakSQL: `alter table pgbase_meta.record_membership_capability drop constraint record_membership_capability_pkey`,
		},
		{
			name:     "missing ordinal unique constraint",
			breakSQL: `alter table pgbase_meta.record_creation_ordinals drop constraint record_creation_ordinals_collection_ordinal_unique`,
		},
		{
			name:     "missing backfill foreign key",
			breakSQL: `alter table pgbase_meta.record_membership_backfills drop constraint record_membership_backfills_counter_fk`,
		},
		{
			name:     "missing capability rollout check",
			breakSQL: `alter table pgbase_meta.record_membership_capability drop constraint record_membership_capability_rollout_state_check`,
		},
		{
			name: "weakened capability rollout check",
			breakSQL: `alter table pgbase_meta.record_membership_capability drop constraint record_membership_capability_rollout_state_check;
				alter table pgbase_meta.record_membership_capability add constraint record_membership_capability_rollout_state_check check (true)`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			if _, err := pool.Exec(context.Background(), testCase.breakSQL); err != nil {
				t.Fatal(err)
			}
			assertInvalidMembershipInstallation(t, runDoctor(t, pool))
		})
	}

	t.Run("ordinal record ID changed from UUID to text", func(t *testing.T) {
		fixture := newMembershipDoctorFixture(t, "dual_write")
		if _, err := fixture.pool.Exec(context.Background(), `alter table pgbase_meta.record_creation_ordinals
			drop constraint record_creation_ordinals_pkey;
			alter table pgbase_meta.record_creation_ordinals alter column record_id type text using record_id::text`); err != nil {
			t.Fatal(err)
		}
		report, err := Run(context.Background(), fixture.pool, migrate.New(fixture.pool, nil))
		if err != nil {
			t.Fatalf("doctor returned raw malformed-catalog error: %v", err)
		}
		assertInvalidMembershipInstallation(t, report)
	})
}

func TestRunFailsClosedOnInconsistentMembershipCollectionCoverage(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		state    string
		breakSQL string
	}{
		{
			name:     "ready collection missing backfill",
			state:    "ready",
			breakSQL: `delete from pgbase_meta.record_membership_backfills`,
		},
		{
			name:     "off collection missing counter and backfill",
			state:    "off",
			breakSQL: `delete from pgbase_meta.collection_creation_counters`,
		},
		{
			name:     "ready capability generation differs from collections",
			state:    "ready",
			breakSQL: `update pgbase_meta.record_membership_capability set generation = 2 where singleton`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMembershipDoctorFixture(t, testCase.state)
			if _, err := fixture.pool.Exec(context.Background(), testCase.breakSQL); err != nil {
				t.Fatal(err)
			}
			assertInvalidMembershipInstallation(t, runDoctor(t, fixture.pool))
		})
	}
}

func TestRunSkipsMembershipScansWhenBaseIDIsInvalid(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		breakSQL string
		wantCode string
	}{
		{
			name:     "missing",
			breakSQL: `alter table app."order" drop column id cascade`,
			wantCode: "collection_column_missing",
		},
		{
			name:     "wrong type",
			breakSQL: `alter table app."order" alter column id type text using id::text`,
			wantCode: "collection_column_type_mismatch",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMembershipDoctorFixture(t, "ready")
			if _, err := fixture.pool.Exec(context.Background(), testCase.breakSQL); err != nil {
				t.Fatal(err)
			}
			report, err := Run(context.Background(), fixture.pool, migrate.New(fixture.pool, nil))
			if err != nil {
				t.Fatalf("doctor replaced catalog finding with membership scan error: %v", err)
			}
			assertFindingCode(t, report, testCase.wantCode)
			for _, diagnostic := range report.Findings {
				if strings.HasPrefix(diagnostic.Code, "record_membership_") {
					t.Fatalf("membership scan added secondary finding after invalid base ID: %#v", report.Findings)
				}
			}
		})
	}
}

func TestRunPreservesExistingFindingOrderBeforeMembershipDrift(t *testing.T) {
	fixture := newMembershipDoctorFixture(t, "ready")
	missingID := uuid.MustParse("0199c000-0000-7000-8000-000000000021")
	orphanID := uuid.MustParse("0199c000-0000-7000-8000-000000000022")
	if _, err := fixture.pool.Exec(context.Background(), `insert into app."order"
		(id, created_at, updated_at) values ($1, $2, $2)`, missingID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `update pgbase_meta.collection_creation_counters
		set last_ordinal = 1, updated_at = $2 where collection_id = $1`,
		fixture.collectionID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, 1)`, fixture.collectionID, orphanID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `update pgbase_meta.schema_state set version = 6
		where singleton`); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, fixture.pool)
	got := make([]string, 0, len(report.Findings))
	for _, diagnostic := range report.Findings {
		got = append(got, diagnostic.Code)
	}
	want := []string{
		"schema_state_stale",
		"record_membership_sidecar_orphaned",
		"record_membership_sidecar_missing",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finding order = %v, want %v", got, want)
	}
	assertBoundedMembershipReport(t, report, fixture.collectionID, missingID, orphanID)
}

type membershipDoctorFixture struct {
	pool         *database.Pool
	collectionID uuid.UUID
	createdAt    time.Time
}

func newMembershipDoctorFixture(t *testing.T, state string) membershipDoctorFixture {
	t.Helper()
	pool := newDoctorPool(t)
	collectionID := insertCollection(t, pool, "order", "order")
	createdAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `create table app."order" (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	);
	create index order_page on app."order" (created_at, id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `delete from pgbase_meta.collection_creation_counters
		where collection_id = $1`, collectionID); err != nil {
		t.Fatal(err)
	}

	switch state {
	case "off":
		if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collection_creation_counters
			(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
			collectionID, createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
			collectionID, createdAt); err != nil {
			t.Fatal(err)
		}
	case "dual_write", "ready":
		if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collection_creation_counters
			(collection_id, generation, last_ordinal, updated_at) values ($1, 1, 0, $2)`,
			collectionID, createdAt); err != nil {
			t.Fatal(err)
		}
		if state == "dual_write" {
			if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.record_membership_backfills
				(collection_id, generation, state, cutover_count, last_assigned_ordinal, updated_at)
				values ($1, 1, 'backfilling', 0, 0, $2)`, collectionID, createdAt); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `update pgbase_meta.record_membership_capability set
				state = 'dual_write', generation = 1, minimum_writer_version = 1,
				cutover_at = $1, cutover_collection_count = 1, cutover_record_count = 0,
				updated_at = $1 where singleton`, createdAt); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.record_membership_backfills
				(collection_id, generation, state, cutover_count, last_assigned_ordinal,
				 validated_base_count, validated_sidecar_count, updated_at, completed_at)
				values ($1, 1, 'ready', 0, 0, 0, 0, $2, $2)`, collectionID, createdAt); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `update pgbase_meta.record_membership_capability set
				state = 'ready', generation = 1, minimum_writer_version = 1,
				cutover_at = $1, cutover_collection_count = 1, cutover_record_count = 0,
				ready_at = $1, ready_collection_count = 1, updated_at = $1 where singleton`,
				createdAt); err != nil {
				t.Fatal(err)
			}
		}
	default:
		t.Fatalf("unsupported membership state %q", state)
	}
	return membershipDoctorFixture{pool: pool, collectionID: collectionID, createdAt: createdAt}
}

func assertBoundedMembershipReport(t *testing.T, report Report, secretIDs ...uuid.UUID) {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	maximumBytes := 192 * (len(report.Findings) + 1)
	if len(encoded) > maximumBytes {
		t.Fatalf("membership diagnostic grew beyond its bound: %d bytes", len(encoded))
	}
	for _, secret := range secretIDs {
		if strings.Contains(string(encoded), secret.String()) {
			t.Fatalf("membership diagnostic leaked an identifier: %s", encoded)
		}
	}
	if strings.Contains(string(encoded), `app."order"`) || strings.Contains(string(encoded), "record_creation_ordinals") {
		t.Fatalf("membership diagnostic leaked SQL details: %s", encoded)
	}
}

func assertInvalidMembershipInstallation(t *testing.T, report Report) {
	t.Helper()
	if report.OK || len(report.Findings) != 1 {
		t.Fatalf("report = %#v, want one blocking finding", report)
	}
	if got := report.Findings[0]; got != finding(
		"record_membership_installation_invalid",
		"Record membership schema installation is incomplete",
	) {
		t.Fatalf("finding = %#v", got)
	}
	assertBoundedMembershipReport(t, report)
}

// addMembershipCollection joins a second collection to an existing fixture in
// the same rollout state, so cross-collection behaviour is exercised instead of
// being inferred from the single-collection fixtures.
func addMembershipCollection(t *testing.T, fixture membershipDoctorFixture, name, state string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	collectionID := insertCollection(t, fixture.pool, name, name)
	table := pgx.Identifier{"app", name}.Sanitize()
	if _, err := fixture.pool.Exec(ctx, `create table `+table+` (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	);
	create index `+pgx.Identifier{name + "_page"}.Sanitize()+` on `+table+` (created_at, id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `delete from pgbase_meta.record_membership_backfills
		where collection_id = $1`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.collection_creation_counters
		set generation = 1, updated_at = $2 where collection_id = $1`, collectionID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	switch state {
	case "dual_write":
		if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, cutover_count, last_assigned_ordinal, updated_at)
			values ($1, 1, 'backfilling', 0, 0, $2)`, collectionID, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
	case "ready":
		if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, cutover_count, last_assigned_ordinal,
			 validated_base_count, validated_sidecar_count, updated_at, completed_at)
			values ($1, 1, 'ready', 0, 0, 0, 0, $2, $2)`, collectionID, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported membership state %q", state)
	}
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.record_membership_capability
		set cutover_collection_count = 2, ready_collection_count = case when state = 'ready' then 2 else 0 end,
		updated_at = $1 where singleton`, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	return collectionID
}

// Drift in several collections is still one operator-facing problem per kind.
// Without the dedup this reports one finding per collection, which is exactly
// the unbounded report the membership checks are supposed to prevent.
func TestRunReportsMembershipSidecarDriftOncePerKindAcrossCollections(t *testing.T) {
	fixture := newMembershipDoctorFixture(t, "ready")
	secondID := addMembershipCollection(t, fixture, "invoice", "ready")
	ctx := context.Background()
	orphanIDs := []uuid.UUID{
		uuid.MustParse("0199c000-0000-7000-8000-0000000000a1"),
		uuid.MustParse("0199c000-0000-7000-8000-0000000000a2"),
	}
	baseIDs := []uuid.UUID{
		uuid.MustParse("0199c000-0000-7000-8000-0000000000b1"),
		uuid.MustParse("0199c000-0000-7000-8000-0000000000b2"),
	}
	for index, collectionID := range []uuid.UUID{fixture.collectionID, secondID} {
		if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.collection_creation_counters
			set last_ordinal = 1, updated_at = $2 where collection_id = $1`,
			collectionID, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
			(collection_id, record_id, ordinal) values ($1, $2, 1)`,
			collectionID, orphanIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	for table, baseID := range map[string]uuid.UUID{`app."order"`: baseIDs[0], `app."invoice"`: baseIDs[1]} {
		if _, err := fixture.pool.Exec(ctx, `insert into `+table+`
			(id, created_at, updated_at) values ($1, $2, $2)`, baseID, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
	}

	report := runDoctor(t, fixture.pool)
	if report.OK || len(report.Findings) != 2 {
		t.Fatalf("report = %#v, want exactly one orphaned and one missing finding", report)
	}
	want := []Finding{
		finding("record_membership_sidecar_orphaned", "A record membership sidecar is missing its base record"),
		finding("record_membership_sidecar_missing", "A base record is missing its required membership sidecar"),
	}
	for index, got := range report.Findings {
		if got != want[index] {
			t.Fatalf("finding[%d] = %#v, want %#v", index, got, want[index])
		}
	}
	assertBoundedMembershipReport(t, report,
		append(append(orphanIDs, baseIDs...), fixture.collectionID, secondID)...)
}

// A partial installation must close the report before any content scan, so an
// operator is told to repair the schema instead of chasing drift findings that
// an incomplete installation cannot be trusted to have produced correctly.
func TestRunClosesInvalidMembershipInstallationBeforeScanningContent(t *testing.T) {
	fixture := newMembershipDoctorFixture(t, "ready")
	ctx := context.Background()
	orphanID := uuid.MustParse("0199c000-0000-7000-8000-0000000000c1")
	baseID := uuid.MustParse("0199c000-0000-7000-8000-0000000000c2")
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.collection_creation_counters
		set last_ordinal = 1, updated_at = $2 where collection_id = $1`,
		fixture.collectionID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, 1)`, fixture.collectionID, orphanID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `insert into app."order"
		(id, created_at, updated_at) values ($1, $2, $2)`, baseID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	// Both scans would now fire; break coverage so the installation check runs first.
	if _, err := fixture.pool.Exec(ctx, `delete from pgbase_meta.record_membership_backfills
		where collection_id = $1`, fixture.collectionID); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, fixture.pool)
	assertInvalidMembershipInstallation(t, report)
	assertBoundedMembershipReport(t, report, orphanID, baseID, fixture.collectionID)
}
