//go:build integration

package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
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

func TestRunAcceptsAnEmptyDatabaseBeforeMembershipRolloutStarts(t *testing.T) {
	fixture := newMembershipDoctorFixture(t, "off")
	if report := runDoctor(t, fixture.pool); !report.OK || len(report.Findings) != 0 {
		t.Fatalf("report = %#v, want empty off database to be healthy", report)
	}
}

func TestRunReportsAnUnstartedMembershipRolloutForExistingRecords(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run("sidecar="+strconv.FormatBool(sidecar), func(t *testing.T) {
			fixture := newMembershipDoctorFixture(t, "off")
			recordID := uuid.MustParse("0199c000-0000-7000-8000-000000000013")
			if _, err := fixture.pool.Exec(context.Background(), `insert into app."order"
				(id, created_at, updated_at) values ($1, $2, $2)`, recordID, fixture.createdAt); err != nil {
				t.Fatal(err)
			}
			if sidecar {
				if _, err := fixture.pool.Exec(context.Background(), `update pgbase_meta.collection_creation_counters
					set last_ordinal = 1 where collection_id = $1`, fixture.collectionID); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.pool.Exec(context.Background(), `insert into pgbase_meta.record_creation_ordinals
					(collection_id, record_id, ordinal) values ($1, $2, 1)`, fixture.collectionID, recordID); err != nil {
					t.Fatal(err)
				}
			}

			want := finding(
				"record_membership_rollout_not_started",
				"Record membership rollout has not started for a database that contains records; follow the membership rollout runbook",
			)
			if sidecar {
				want = finding("record_membership_installation_invalid", "Record membership schema installation is incomplete")
			}
			report := runDoctor(t, fixture.pool)
			if report.OK || len(report.Findings) != 1 {
				t.Fatalf("report = %#v, want one actionable rollout finding", report)
			}
			if got := report.Findings[0]; got != want {
				t.Fatalf("finding = %#v", got)
			}
			assertBoundedMembershipReport(t, report, recordID, fixture.collectionID)
		})
	}
}

func TestRunFailsClosedOnPartialMembershipInstallation(t *testing.T) {
	// A history torn between v6 and v7 is no longer one of the two the current
	// preparatory artifact declares, so the migration gate names the real problem
	// before any membership object is inspected. Partial installations on a
	// declared history stay covered by the drop-one-object cases below.
	t.Run("v6 history with a v7 fragment", func(t *testing.T) {
		databaseURL := testutil.NewDatabase(t)
		migrations := system.Migrations()
		if len(migrations) < 7 || migrations[6].Version != 7 {
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
		assertFindingCode(t, runDoctor(t, pool), "migration_not_current")
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
		// Each of these leaves every column, constraint, index and CHECK hash
		// intact while silently swallowing or hiding the very rows the coverage
		// query and the sidecar scans read.
		{
			name: "trigger swallowing sidecar inserts",
			breakSQL: `create function pgbase_meta.doctor_probe() returns trigger
				language plpgsql as $$ begin return null; end $$;
				create trigger doctor_probe before insert on pgbase_meta.record_creation_ordinals
				for each row execute function pgbase_meta.doctor_probe()`,
		},
		{
			name:     "rule discarding sidecar inserts",
			breakSQL: `create rule doctor_probe as on insert to pgbase_meta.record_creation_ordinals do instead nothing`,
		},
		{
			name:     "row level security enabled on the sidecar",
			breakSQL: `alter table pgbase_meta.record_creation_ordinals enable row level security`,
		},
		{
			name:     "row level security forced on the capability row",
			breakSQL: `alter table pgbase_meta.record_membership_capability force row level security`,
		},
		{
			name:     "row level security enabled on the writer leases",
			breakSQL: `alter table pgbase_meta.runtime_writer_leases enable row level security`,
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
	// beginCutover bumps only the capability generation: per-collection
	// counters and backfills stay at the 'off' values until finishCutover
	// moves the rollout to dual_write.
	case "off", "cutover":
		if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collection_creation_counters
			(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
			collectionID, createdAt); err != nil {
			t.Fatal(err)
		}
		if state == "cutover" {
			if _, err := pool.Exec(context.Background(), `update pgbase_meta.record_membership_capability set
				state = 'cutover', generation = 1, minimum_writer_version = 1,
				cursor_issue_version = 4, cutover_at = $1, updated_at = $1 where singleton`,
				createdAt); err != nil {
				t.Fatal(err)
			}
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

// pinnedDoctorMessages is the exact text doctor must emit for every code these
// tests can produce. A code missing from here is a deliberate stop: whoever
// adds a finding has to state its constant message.
var pinnedDoctorMessages = map[string]string{
	"schema_state_stale":                     "Schema state does not match schema migration history",
	"record_membership_installation_invalid": "Record membership schema installation is incomplete",
	"record_membership_rollout_not_started":  "Record membership rollout has not started for a database that contains records; follow the membership rollout runbook",
	"record_membership_sidecar_orphaned":     "A record membership sidecar is missing its base record",
	"record_membership_sidecar_missing":      "A base record is missing its required membership sidecar",
}

// assertBoundedMembershipReport bounds what reaches an operator, and nothing
// else. It rebuilds the report from each finding's code alone and demands the
// encoding match byte for byte, so a message, severity or field that ever
// carries a collection name, row count or SQL fragment fails here even though
// the leak scans below would still pass.
//
// It does NOT bound the work doctor performs: the sidecar queries stay
// O(total records) and hold AccessShare on every collection table however small
// this report is. Only TestRunTakesNoCollectionLocksDuringCutover constrains
// that side.
func assertBoundedMembershipReport(t *testing.T, report Report, secretIDs ...uuid.UUID) {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secretIDs {
		if strings.Contains(string(encoded), secret.String()) {
			t.Fatalf("membership diagnostic leaked an identifier: %s", encoded)
		}
	}
	if strings.Contains(string(encoded), `app."order"`) || strings.Contains(string(encoded), "record_creation_ordinals") {
		t.Fatalf("membership diagnostic leaked SQL details: %s", encoded)
	}
	rebuilt := Report{OK: report.OK, Findings: make([]Finding, 0, len(report.Findings))}
	for _, got := range report.Findings {
		message, pinned := pinnedDoctorMessages[got.Code]
		if !pinned {
			t.Fatalf("finding %q has no pinned message; add its constant to pinnedDoctorMessages", got.Code)
		}
		rebuilt.Findings = append(rebuilt.Findings, finding(got.Code, message))
	}
	want, err := json.Marshal(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("membership diagnostic carries data beyond its code:\n got %s\nwant %s", encoded, want)
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
// Both kinds are present in the first collection here, so the scan stops there;
// TestRunReportsSplitMembershipSidecarDriftOncePerKind covers the case where
// the loop has to keep going.
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

// A collection table dropped after doctor took its snapshot is a benign race,
// not drift. A SQL read of pg_class still reports the table, so
// readMembershipCollections hands out a collection whose probe then resolves
// the identifier against the live catalog and raises 42P01. The run has to skip
// that collection, keep scanning the rest, and stay committable.
func TestRunSkipsCollectionsDroppedAfterTheDoctorSnapshot(t *testing.T) {
	ctx := context.Background()
	fixture := newMembershipDoctorFixture(t, "ready")
	addMembershipCollection(t, fixture, "invoice", "ready")
	orphanID := uuid.MustParse("0199c000-0000-7000-8000-0000000000e1")
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.collection_creation_counters
		set last_ordinal = 1, updated_at = $2 where collection_id = $1`,
		fixture.collectionID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, 1)`,
		fixture.collectionID, orphanID); err != nil {
		t.Fatal(err)
	}
	status, err := migrate.New(fixture.pool, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	connection, err := fixture.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	report := Report{Findings: []Finding{}}
	if err := pinDeparseStyles(ctx, tx); err != nil {
		t.Fatal(err)
	}
	// The snapshot is taken by the first read, not by BEGIN or SET LOCAL. Drop
	// the table before this and readMembershipCollections never returns the
	// collection at all, so the race under test never happens.
	if _, err := checkSchemaState(ctx, tx, &report); err != nil {
		t.Fatal(err)
	}
	// "invoice" sorts before "order", so the dropped collection is probed first
	// and the surviving orphan is found only if the scan resumed after it.
	if _, err := fixture.pool.Exec(ctx, `drop table app."invoice"`); err != nil {
		t.Fatal(err)
	}

	if err := checkRecordMembership(ctx, tx, status, &report); err != nil {
		t.Fatalf("doctor failed on a collection dropped after its snapshot: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("doctor transaction did not survive the undefined-table error: %v", err)
	}
	want := []Finding{finding(
		"record_membership_sidecar_orphaned",
		"A record membership sidecar is missing its base record",
	)}
	if !reflect.DeepEqual(report.Findings, want) {
		t.Fatalf("findings = %#v, want %#v", report.Findings, want)
	}
	assertBoundedMembershipReport(t, report, orphanID, fixture.collectionID)
}

// One kind of drift per collection keeps the scan running through both, which
// is the only shape that pins where the findings are appended: the scan
// accumulates into two booleans and appends once after the loop, so an append
// moved inside it reports the orphan again for every later collection.
func TestRunReportsSplitMembershipSidecarDriftOncePerKind(t *testing.T) {
	ctx := context.Background()
	fixture := newMembershipDoctorFixture(t, "ready")
	invoiceID := addMembershipCollection(t, fixture, "invoice", "ready")
	orphanID := uuid.MustParse("0199c000-0000-7000-8000-0000000000f1")
	baseID := uuid.MustParse("0199c000-0000-7000-8000-0000000000f2")
	// "invoice" sorts first and carries only the orphaned sidecar; "order"
	// carries only the unpaired base record, so neither collection alone
	// satisfies the scan's stop condition.
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.collection_creation_counters
		set last_ordinal = 1, updated_at = $2 where collection_id = $1`,
		invoiceID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal) values ($1, $2, 1)`, invoiceID, orphanID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `insert into app."order"
		(id, created_at, updated_at) values ($1, $2, $2)`, baseID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, fixture.pool)
	want := []Finding{
		finding("record_membership_sidecar_orphaned", "A record membership sidecar is missing its base record"),
		finding("record_membership_sidecar_missing", "A base record is missing its required membership sidecar"),
	}
	if !reflect.DeepEqual(report.Findings, want) {
		t.Fatalf("findings = %#v, want exactly %#v", report.Findings, want)
	}
	assertBoundedMembershipReport(t, report, orphanID, baseID, invoiceID, fixture.collectionID)
}

// cutover is a write barrier: internal/membership/writer.go returns
// ErrUnavailable for every write while it is set, so no sidecar can exist and
// base rows without one are the expected state rather than drift.
func TestRunAcceptsUnpairedBaseRecordsDuringCutover(t *testing.T) {
	fixture := newMembershipDoctorFixture(t, "cutover")
	baseID := uuid.MustParse("0199c000-0000-7000-8000-0000000000d1")
	if _, err := fixture.pool.Exec(context.Background(), `insert into app."order"
		(id, created_at, updated_at) values ($1, $2, $2)`, baseID, fixture.createdAt); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, fixture.pool)
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("report = %#v, want the cutover write barrier to read as healthy", report)
	}
	assertBoundedMembershipReport(t, report, baseID, fixture.collectionID)
}

// Findings alone cannot tell cutover's early return from the dual_write scan:
// the coverage query already proves no sidecar exists, so the orphan probe
// finds nothing and the missing probe is gated on ready. What differs is the
// lock footprint. The sidecar scans are the only doctor statements that touch a
// collection table, so during cutover an operator holding ACCESS EXCLUSIVE on a
// collection must not be able to stall a doctor run at all.
func TestRunTakesNoCollectionLocksDuringCutover(t *testing.T) {
	ctx := context.Background()
	fixture := newMembershipDoctorFixture(t, "cutover")
	blocker, err := fixture.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	blocking, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocking.Rollback(ctx) }()
	if _, err := blocking.Exec(ctx, `lock table app."order" in access exclusive mode`); err != nil {
		t.Fatal(err)
	}

	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	report, err := Run(bounded, fixture.pool, migrate.New(fixture.pool, nil))
	if err != nil {
		t.Fatalf("doctor waited on a collection lock it must not take during cutover: %v", err)
	}
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("report = %#v", report)
	}
}
