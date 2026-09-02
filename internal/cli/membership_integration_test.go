//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
	"github.com/webisnoteasy/PgBase/pgbase"
)

const (
	upgradeOrigin     = "http://pgbase.test"
	upgradeEmail      = "operator@example.test"
	upgradePassword   = "correct horse battery staple"
	upgradeCollection = "upgraded_records"
	// More than one backfill batch, so a command that ran a single pass and
	// stopped would leave the rollout provably incomplete.
	upgradeRecords = 300
)

// membershipCommand runs one operator invocation and reports its exit code with
// whatever it wrote, so a test asserts on the process contract rather than on a
// service call it made itself.
func membershipCommand(t *testing.T, databaseURL string, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), append([]string{"membership"}, args...),
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Logf("membership %s exit=%d stderr=%q", strings.Join(args, " "), code, stderr.String())
	}
	return code, stdout.String()
}

func requireMembershipCommand(t *testing.T, databaseURL string, args ...string) string {
	t.Helper()
	code, stdout := membershipCommand(t, databaseURL, args...)
	if code != 0 {
		t.Fatalf("membership %s exit=%d stdout=%q", strings.Join(args, " "), code, stdout)
	}
	return stdout
}

func requireMembershipRefusal(t *testing.T, databaseURL string, args ...string) {
	t.Helper()
	code, stdout := membershipCommand(t, databaseURL, args...)
	if code != 1 {
		t.Fatalf("membership %s exit=%d stdout=%q, want an operational refusal", strings.Join(args, " "), code, stdout)
	}
}

func readMembershipReport(t *testing.T, databaseURL string) membershipReport {
	t.Helper()
	stdout := requireMembershipCommand(t, databaseURL, "status", "--json")
	var report membershipReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("status report=%q err=%v", stdout, err)
	}
	return report
}

// The defect this covers: a database that already held records could not be
// carried through the rollout with the shipped binary, so Release C's read path
// refused every list forever. The whole sequence below therefore runs through
// the operator command, never through a service call and never through SQL that
// moves the capability.
func TestMembershipUpgradesAPopulatedDatabaseThroughTheDocumentedRollout(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	// Byte-identical to the history the previous release ran on, then populated
	// the way that release would have populated it.
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:5])
	seedPopulatedDeployment(t, databaseURL)
	requireMigrateUp(t, databaseURL)

	app := startUpgradedApp(t, databaseURL)
	requireReadiness(t, app)
	session := upgradeSession(t, app)
	// The audit's reproduction, asserted rather than assumed: the instance is
	// serving and its readiness is 200, and every list still fails closed.
	if status, _ := listUpgradedRecords(t, app, session, ""); status != http.StatusServiceUnavailable {
		t.Fatalf("list before the rollout = %d, want the fail-closed 503", status)
	}

	initial := readMembershipReport(t, databaseURL)
	if initial.State != "off" || initial.CursorIssueVersion != 4 || len(initial.Witnesses) != 1 {
		t.Fatalf("status before the rollout = %#v", initial)
	}
	witness := initial.Witnesses[0]

	// Hold point: promotion cannot skip the cutover barrier.
	requireMembershipRefusal(t, databaseURL, "promote-ready")
	// Hold point: an irreversible step refuses a proof that is not the live
	// fleet, so a stale or invented witness cannot open the barrier.
	requireMembershipRefusal(t, databaseURL, "cutover",
		"--witness", uuid.MustParse("0199c500-0000-7000-8000-0000000000ff").String()+":3")

	cutover := requireMembershipCommand(t, databaseURL, "cutover", "--witness", witness)
	if !strings.Contains(cutover, "cutover generation=1 collections=1 records=300") {
		t.Fatalf("cutover receipt = %q", cutover)
	}
	// Rerunning a completed step reports the receipt it already stamped.
	if repeated := requireMembershipCommand(t, databaseURL, "cutover", "--witness", witness); repeated != cutover {
		t.Fatalf("second cutover = %q, want the first receipt %q", repeated, cutover)
	}

	// Hold point: promotion refuses a backfill that has not validated its counts.
	requireMembershipRefusal(t, databaseURL, "promote-ready")

	backfill := requireMembershipCommand(t, databaseURL, "backfill")
	if !strings.Contains(backfill, "done=true") || strings.Count(backfill, "\n") < 3 {
		t.Fatalf("backfill progress = %q, want several resumable passes ending done", backfill)
	}
	// Rerunning the completed backfill is safe and does nothing.
	if repeated := requireMembershipCommand(t, databaseURL, "backfill"); strings.Count(repeated, "\n") != 1 ||
		!strings.Contains(repeated, "scanned=0 inserted=0 collectionReady=false done=true") {
		t.Fatalf("second backfill = %q", repeated)
	}

	ready := requireMembershipCommand(t, databaseURL, "promote-ready")
	if !strings.Contains(ready, "ready generation=1 collections=1") {
		t.Fatalf("promote-ready receipt = %q", ready)
	}
	if repeated := requireMembershipCommand(t, databaseURL, "promote-ready"); repeated != ready {
		t.Fatalf("second promote-ready = %q, want the first receipt %q", repeated, ready)
	}

	requireMembershipCommand(t, databaseURL, "enable-v5", "--witness", witness)
	if enabled := readMembershipReport(t, databaseURL); enabled.CursorIssueVersion != 5 ||
		enabled.MinimumWriterVersion != 2 || enabled.V5EnabledAt == nil || enabled.DecodeV4Until == nil {
		t.Fatalf("status after enable-v5 = %#v", enabled)
	}

	// The documented rollback: while v4 is not retired, issuance goes back.
	requireMembershipCommand(t, databaseURL, "disable-v5")
	rolledBack := readMembershipReport(t, databaseURL)
	if rolledBack.State != "ready" || rolledBack.CursorIssueVersion != 4 ||
		rolledBack.MinimumWriterVersion != 1 || rolledBack.V5EnabledAt != nil ||
		rolledBack.DecodeV4Until != nil || rolledBack.V4RetiredAt != nil {
		t.Fatalf("status after disable-v5 = %#v", rolledBack)
	}
	requireMembershipCommand(t, databaseURL, "enable-v5", "--witness", witness)

	// Hold point: retirement refuses while a v4 cursor could still be live.
	requireMembershipRefusal(t, databaseURL, "retire-v4", "--witness", witness)
	drainUpgradeCursorTTL(t, databaseURL)
	retired := requireMembershipCommand(t, databaseURL, "retire-v4", "--witness", witness)
	if repeated := requireMembershipCommand(t, databaseURL, "retire-v4", "--witness", witness); repeated != retired {
		t.Fatalf("second retire-v4 = %q, want the first receipt %q", repeated, retired)
	}
	// The rollback window is closed for good once retirement has stamped the row.
	requireMembershipRefusal(t, databaseURL, "disable-v5")

	final := readMembershipReport(t, databaseURL)
	if final.State != "ready" || final.CursorIssueVersion != 5 || final.V4RetiredAt == nil ||
		final.MinimumWriterVersion != 2 {
		t.Fatalf("status after retirement = %#v", final)
	}

	status, body := listUpgradedRecords(t, app, session, "")
	if status != http.StatusOK {
		t.Fatalf("list after the rollout = %d body=%s", status, body)
	}
	if visible := countListedRecords(t, app, session); visible != upgradeRecords {
		t.Fatalf("listed %d pre-existing records, want %d", visible, upgradeRecords)
	}
}

// An operator reads status before anything is deployed, and the answer they
// need there is that no writer holds a lease — which is why a transition would
// be refused. Reporting the empty fleet as a failure would hide exactly that.
func TestMembershipStatusReportsAnEmptyFleetWithoutFailing(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareDoctorDatabase(t, databaseURL)
	report := readMembershipReport(t, databaseURL)
	if len(report.Witnesses) != 0 {
		t.Fatalf("status on an undeployed database = %#v", report)
	}
	// A migrated database with no collection and no record is the fresh install
	// the runtime bootstraps, so nothing here has been through the rollout.
	if report.State != "off" || report.CursorIssueVersion != 4 {
		t.Fatalf("status before any deployment = %#v", report)
	}
}

// The rollout's whole safety story is the writer-version fence: every
// irreversible transition raises minimum_writer_version, and refuses to move
// while any instance below the new floor still holds a lease. A fleet that is
// uniformly at the head release never exercises it — nothing there is ever too
// old — so this drives the first fence, the Release A floor the cutover
// requires, with a genuinely mixed fleet: a Release A0 process still holding
// its lease alongside the deployed current release.
//
// The drain is waited out rather than forced. The lease TTL is the only thing
// that removes a row, and expiring one by hand would replace the mechanism
// operators actually rely on with an assertion about the test's own SQL.
func TestMembershipCutoverRefusesUntilTheOlderReleaseDrains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:5])
	seedPopulatedDeployment(t, databaseURL)
	requireMigrateUp(t, databaseURL)

	// Registered before the current release starts, so the older lease is
	// already running down while the operator reads status. Nothing renews it:
	// this is the instance the runbook tells the operator to drain.
	stale := requireRolloutWriter(t, databaseURL, membership.WriterA0)
	staleWitness := membershipWitness(stale.FleetMember())
	app := startUpgradedApp(t, databaseURL)
	requireReadiness(t, app)

	mixed := readMembershipReport(t, databaseURL)
	headWitness := deployedFleetWitness(t, mixed, staleWitness)
	if !slices.Equal(mixed.Witnesses, sortedWitnesses(staleWitness, headWitness)) ||
		mixed.State != "off" || mixed.MinimumWriterVersion != 0 {
		t.Fatalf("status over a mixed fleet = %#v", mixed)
	}

	// The honest proof: exactly the live lease set, named as status reports it.
	// It is still refused, because one member is below the Release A floor.
	requireMembershipRefusal(t, databaseURL, "cutover",
		"--witness", staleWitness, "--witness", headWitness)
	// The proof an operator produces by overlooking the instance they forgot to
	// drain. The transition rereads the lease set under its own locks, so
	// leaving a member out cannot open the barrier — and here the version floor
	// would refuse this proof on its own too, which is why the set half of the
	// proof is probed separately, over a fleet nothing in is too old, in
	// TestMembershipEnableV5RefusesAMixedFleetAndTheRollbackReopensItToReleaseA.
	requireMembershipRefusal(t, databaseURL, "cutover", "--witness", headWitness)
	if held := readMembershipReport(t, databaseURL); held.State != "off" || held.Generation != 0 {
		t.Fatalf("a refused cutover moved the capability: %#v", held)
	}

	// The deployed instance has to still be here afterwards. Its lease is a
	// minute long and this wait outlives it, so a fleet that drained to nothing
	// would mean the running application never renewed.
	drained := drainWriterFleet(t, databaseURL, headWitness)
	if drained.State != "off" || drained.Generation != 0 {
		t.Fatalf("status after the drain = %#v", drained)
	}
	cutover := requireMembershipCommand(t, databaseURL, "cutover", "--witness", headWitness)
	if !strings.Contains(cutover, "cutover generation=1 collections=1 records=300") {
		t.Fatalf("cutover receipt after the drain = %q", cutover)
	}

	// The process the fleet moved past must stop rather than renew. Its lease
	// row is still there — expiry removes nothing — so a writer that could
	// renew would silently rejoin a fleet the transition has already left
	// behind, and would then be writing records below the floor.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	stopped := make(chan error, 1)
	go func() { stopped <- stale.Run(runCtx) }()
	select {
	case err := <-stopped:
		if !errors.Is(err, membership.ErrWriterTooOld) {
			t.Fatalf("the passed Release A0 writer stopped with %v, want %v", err, membership.ErrWriterTooOld)
		}
	case <-time.After(30 * time.Second):
		cancelRun()
		<-stopped
		t.Fatal("the passed Release A0 writer kept renewing after the cutover raised the floor")
	}
	if after := readMembershipReport(t, databaseURL); !slices.Equal(after.Witnesses, []string{headWitness}) {
		t.Fatalf("fleet after the passed writer tried to resume = %v", after.Witnesses)
	}
}

// The second fence is the Release B floor `enable-v5` requires, and it is the
// one a mixed fleet can reach on purpose: the cutover leaves the floor at
// Release A, so a Release A instance may legitimately hold a lease there.
//
// The rollback is covered in the same test because a mixed fleet is what it
// produces rather than something it is performed under: while v5 issuance is
// on the floor is Release B and no Release A instance can hold a lease at all,
// so the only way back to a mixed fleet is `disable-v5` dropping the floor.
func TestMembershipEnableV5RefusesAMixedFleetAndTheRollbackReopensItToReleaseA(t *testing.T) {
	t.Parallel()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:5])
	seedPopulatedDeployment(t, databaseURL)
	requireMigrateUp(t, databaseURL)

	app := startUpgradedApp(t, databaseURL)
	requireReadiness(t, app)
	session := upgradeSession(t, app)
	headWitness := deployedFleetWitness(t, readMembershipReport(t, databaseURL))
	// A second instance of the same release, so the fleet is more than one
	// process before anything in it is out of date.
	peer := requireRolloutWriter(t, databaseURL, membership.WriterC)
	peerWitness := membershipWitness(peer.FleetMember())

	// The other half of the proof, on its own: every live writer here is at or
	// above the Release A floor, so nothing about the versions can refuse this.
	// What refuses it is that the operator named some of the fleet rather than
	// all of it, which is the confirmation the proof exists to demand.
	requireMembershipRefusal(t, databaseURL, "cutover", "--witness", headWitness)
	requireMembershipCommand(t, databaseURL, "cutover",
		"--witness", headWitness, "--witness", peerWitness)
	requireMembershipCommand(t, databaseURL, "backfill")
	requireMembershipCommand(t, databaseURL, "promote-ready")

	// Release A is admitted at this floor. That is what makes the fleet mixed,
	// and it is the state the runbook's "deploy Release B to every instance
	// before enabling the capability" exists to get out of.
	releaseA := requireRolloutWriter(t, databaseURL, membership.WriterA)
	releaseAWitness := membershipWitness(releaseA.FleetMember())
	mixed := readMembershipReport(t, databaseURL)
	if !slices.Equal(mixed.Witnesses, sortedWitnesses(releaseAWitness, headWitness, peerWitness)) ||
		mixed.State != "ready" || mixed.MinimumWriterVersion != 1 {
		t.Fatalf("status over a mixed fleet at the Release A floor = %#v", mixed)
	}

	// The whole live fleet, named honestly, and still refused: one member is
	// below the Release B floor enable-v5 requires.
	requireMembershipRefusal(t, databaseURL, "enable-v5", "--witness", releaseAWitness,
		"--witness", headWitness, "--witness", peerWitness)
	requireMembershipRefusal(t, databaseURL, "enable-v5", "--witness", headWitness)
	if held := readMembershipReport(t, databaseURL); held.CursorIssueVersion != 4 ||
		held.MinimumWriterVersion != 1 || held.V5EnabledAt != nil {
		t.Fatalf("a refused enable-v5 moved the capability: %#v", held)
	}

	// Only the running application renews; the two registered runtimes each
	// hold one lease and let it run out, which is the drain the runbook names.
	drainWriterFleet(t, databaseURL, headWitness)
	requireMembershipCommand(t, databaseURL, "enable-v5", "--witness", headWitness)
	enabled := readMembershipReport(t, databaseURL)
	if enabled.CursorIssueVersion != 5 || enabled.MinimumWriterVersion != 2 ||
		enabled.V5EnabledAt == nil || enabled.DecodeV4Until == nil {
		t.Fatalf("status after the drained enable-v5 = %#v", enabled)
	}
	// Enabling v5 issuance is not what opens the read path. Retirement is, and
	// it has not happened, so the deployed release still fails lists closed.
	if status, _ := listUpgradedRecords(t, app, session, ""); status != http.StatusServiceUnavailable {
		t.Fatalf("list after enable-v5 but before retirement = %d, want the fail-closed 503", status)
	}

	// At the Release B floor the fleet cannot become mixed again: a Release A
	// instance is refused its lease at registration, before it ever writes.
	if _, err := openRolloutWriter(t, databaseURL, membership.WriterA); !errors.Is(err, membership.ErrWriterTooOld) {
		t.Fatalf("registering Release A under the Release B floor = %v, want %v", err, membership.ErrWriterTooOld)
	}

	// The documented rollback drops the floor back to Release A, and that is
	// exactly what readmits a Release A instance.
	requireMembershipCommand(t, databaseURL, "disable-v5")
	rolledBack := readMembershipReport(t, databaseURL)
	if rolledBack.State != "ready" || rolledBack.CursorIssueVersion != 4 ||
		rolledBack.MinimumWriterVersion != 1 || rolledBack.V5EnabledAt != nil ||
		rolledBack.DecodeV4Until != nil || rolledBack.V4RetiredAt != nil {
		t.Fatalf("status after the rollback = %#v", rolledBack)
	}
	// What the rollback means for the still-deployed head release. Release C
	// carries no v4 cursor, and this is the transition that could put issuance
	// back on v4, so the hazard the runbook orders retirement to close is a
	// list that serves a truncated first page with no continuation. It does not
	// open: the read path fences on v4_retired_at, not on the issue version, so
	// a list is refused here exactly as it was before v5 was ever enabled.
	if status, _ := listUpgradedRecords(t, app, session, ""); status != http.StatusServiceUnavailable {
		t.Fatalf("list after the rollback = %d, want the fail-closed 503", status)
	}

	rejoined := requireRolloutWriter(t, databaseURL, membership.WriterA)
	rejoinedWitness := membershipWitness(rejoined.FleetMember())
	if reopened := readMembershipReport(t, databaseURL); !slices.Equal(
		reopened.Witnesses, sortedWitnesses(rejoinedWitness, headWitness)) {
		t.Fatalf("fleet after the rollback readmitted Release A = %v", reopened.Witnesses)
	}
	// The price of the rollback: re-enabling costs another drain, because the
	// same fence applies to the fleet the rollback let back in.
	requireMembershipRefusal(t, databaseURL, "enable-v5",
		"--witness", rejoinedWitness, "--witness", headWitness)
	requireMembershipRefusal(t, databaseURL, "enable-v5", "--witness", headWitness)
}

// requireMigrateUp carries the seeded pre-membership history to the release
// under test, through the same command the runbook names.
func requireMigrateUp(t *testing.T, databaseURL string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"migrate", "up"}, migrationEnv(databaseURL),
		IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("migrate up exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// openRolloutWriter registers one process identity at the named release over
// its own pool, which is what a deployed instance of that artifact does at
// startup. The release is a parameter of registration, so one test process can
// hold the mixed fleet the fence exists for without a historical binary.
//
// The pool is held at its two-connection minimum on purpose. These runtimes
// register and then wait out a lease, and a default-sized pool for each of them
// would hold a large share of the server's connections for the whole of a drain
// while the rest of the suite runs against the same server.
func openRolloutWriter(
	t *testing.T, databaseURL string, version membership.WriterVersion,
) (*membership.Runtime, error) {
	t.Helper()
	databaseURL, err := rolloutWriterDatabaseURL(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return membership.RegisterWriter(context.Background(), membership.New(pool), version)
}

func rolloutWriterDatabaseURL(databaseURL string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "2")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func TestRolloutWriterDatabaseURLAddsPoolLimit(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"postgres://postgres:postgres@localhost/postgres",
		"postgres://postgres:postgres@localhost/postgres?sslmode=disable",
	} {
		got, err := rolloutWriterDatabaseURL(input)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Path != "/postgres" || parsed.Query().Get("pool_max_conns") != "2" {
			t.Fatalf("rollout writer URL = %q", got)
		}
	}
}

func requireRolloutWriter(
	t *testing.T, databaseURL string, version membership.WriterVersion,
) *membership.Runtime {
	t.Helper()
	runtime, err := openRolloutWriter(t, databaseURL, version)
	if err != nil {
		t.Fatalf("register a writer at release %d: %v", int(version), err)
	}
	return runtime
}

// membershipWitness renders the --witness spelling of a registered runtime,
// which is what status reports for it and what the next transition requires
// back. It is built from the runtime rather than read out of the report by
// position: instance ids are time-ordered UUIDv7 values, so which row comes
// first is an accident of registration order.
func membershipWitness(member membership.FleetMember) string {
	return member.InstanceID.String() + ":" + strconv.Itoa(int(member.Version))
}

// deployedFleetWitness names the running application's own witness: the one
// the report carries that the test did not register itself.
func deployedFleetWitness(t *testing.T, report membershipReport, registered ...string) string {
	t.Helper()
	var deployed []string
	for _, witness := range report.Witnesses {
		if !slices.Contains(registered, witness) {
			deployed = append(deployed, witness)
		}
	}
	if len(deployed) != 1 || !strings.HasSuffix(deployed[0], ":3") {
		t.Fatalf("fleet = %v, want one deployed Release C instance beside %v", report.Witnesses, registered)
	}
	return deployed[0]
}

// sortedWitnesses puts expected witnesses in the order the fleet read produces.
// The lease query orders by instance_id, and a canonical UUID string orders the
// same way its bytes do.
func sortedWitnesses(witnesses ...string) []string {
	ordered := slices.Clone(witnesses)
	slices.Sort(ordered)
	return ordered
}

// drainWriterFleet waits for the older releases to leave the fleet the way the
// runbook has an operator wait: by rereading the operator status until it
// reports exactly the instances that are still deployed. It expires nothing
// itself. The writer lease is a minute long and is stamped from the database
// clock, so the deadline leaves room for a full TTL and for skew between the
// two clocks; a wait this long also outlives the deployed instance's own lease,
// which is what makes it a check on the running heartbeat as well.
func drainWriterFleet(t *testing.T, databaseURL string, deployed ...string) membershipReport {
	t.Helper()
	want := sortedWitnesses(deployed...)
	deadline := time.Now().Add(100 * time.Second)
	for {
		report := readMembershipReport(t, databaseURL)
		if slices.Equal(report.Witnesses, want) {
			return report
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer fleet drained to %v, want %v", report.Witnesses, want)
		}
		time.Sleep(2 * time.Second)
	}
}

// startUpgradedApp stands in for the `pgbase serve` deployment the rollout
// needs: the transitions require live writer leases, and only a running
// instance holds one.
func startUpgradedApp(t *testing.T, databaseURL string) *pgbase.App {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	app, err := pgbase.New(ctx, pgbase.Options{
		DatabaseURL: databaseURL,
		TokenSecret: make([]byte, 32),
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		cancel()
		t.Fatalf("construct the upgraded application: %v", err)
	}
	if err := app.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start the upgraded application: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := app.Close(closeCtx); err != nil {
			t.Errorf("close the upgraded application: %v", err)
		}
	})
	return app
}

func requireReadiness(t *testing.T, app *pgbase.App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		if response.Code == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness never returned 200: status=%d body=%s", response.Code, response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// upgradeSession authenticates the way an operator's client does, so the list
// below is the same authenticated read the audit reproduced.
func upgradeSession(t *testing.T, app *pgbase.App) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, upgradeOrigin+"/api/admin/auth/login",
		strings.NewReader(`{"email":"`+upgradeEmail+`","password":"`+upgradePassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", upgradeOrigin)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("administrator login = %d body=%s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "pgbase_admin" && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("login set no administrator session cookie: %v", response.Result().Cookies())
	return ""
}

func listUpgradedRecords(t *testing.T, app *pgbase.App, session, cursor string) (int, string) {
	t.Helper()
	target := upgradeOrigin + "/api/v1/collections/" + upgradeCollection + "/records?limit=100"
	if cursor != "" {
		target += "&cursor=" + cursor
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: "pgbase_admin", Value: session})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

// countListedRecords walks every page, because the omission this rollout exists
// to remove is a page boundary that silently drops a record.
func countListedRecords(t *testing.T, app *pgbase.App, session string) int {
	t.Helper()
	total, cursor := 0, ""
	for page := 0; page < 10; page++ {
		status, body := listUpgradedRecords(t, app, session, cursor)
		if status != http.StatusOK {
			t.Fatalf("list page %d = %d body=%s", page, status, body)
		}
		var decoded struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("decode list page %d: %v body=%s", page, err, body)
		}
		total += len(decoded.Items)
		if decoded.NextCursor == "" {
			return total
		}
		cursor = decoded.NextCursor
	}
	t.Fatal("list never reached its last page")
	return 0
}

// seedPopulatedDeployment writes the catalog and records the previous release
// would have left behind. It is the fixture for the database being upgraded;
// nothing here touches the membership capability, which does not exist yet on
// this history and is what the operator command has to move.
func seedPopulatedDeployment(t *testing.T, databaseURL string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close populated seed connection: %v", err)
		}
	}()
	collectionID := uuid.MustParse("0199c500-0000-7000-8000-000000000001")
	// The catalog version a collection create would have left behind. A v5
	// cursor carries it, so a fixture that skipped it would not be the database
	// the previous release actually produced.
	if _, err := conn.Exec(ctx, `update pgbase_meta.schema_state
		set version = 1, updated_at = transaction_timestamp() where singleton`); err != nil {
		t.Fatalf("advance the catalog version: %v", err)
	}
	if _, err := conn.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $2, 'active', transaction_timestamp(), transaction_timestamp())`,
		collectionID, upgradeCollection); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
	if _, err := conn.Exec(ctx, `create table app.`+upgradeCollection+` (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null)`); err != nil {
		t.Fatalf("seed collection table: %v", err)
	}
	if _, err := conn.Exec(ctx, `insert into app.`+upgradeCollection+` (id, created_at, updated_at)
		select gen_random_uuid(),
			transaction_timestamp() - make_interval(secs => generated.ordinal),
			transaction_timestamp()
		from generate_series(1, $1) as generated(ordinal)`, upgradeRecords); err != nil {
		t.Fatalf("seed records: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Execute(ctx, []string{"admin", "create", "--email", upgradeEmail, "--password-stdin"},
		migrationEnv(databaseURL), IO{
			Stdin: strings.NewReader(upgradePassword + "\n"), Stdout: &stdout, Stderr: &stderr,
		}); code != 0 {
		t.Fatalf("admin create exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// drainUpgradeCursorTTL moves the rollout's timeline into the past, together,
// so the stamps stay in the order the v7 CHECK constraints and the read path
// both require. It moves time, not state: retirement still re-reads the lease
// set under its own locks and still compares the deadline against the database
// clock. A test cannot wait out a fifteen-minute drain, and forcing the
// capability itself would be the shortcut this whole change exists to remove.
func drainUpgradeCursorTTL(t *testing.T, databaseURL string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close cursor drain connection: %v", err)
		}
	}()
	if _, err := conn.Exec(ctx, `update pgbase_meta.record_membership_capability set
		cutover_at = transaction_timestamp() - interval '20 minutes',
		ready_at = transaction_timestamp() - interval '19 minutes',
		v5_enabled_at = transaction_timestamp() - interval '16 minutes',
		v4_decode_until = transaction_timestamp() - interval '1 minute',
		updated_at = transaction_timestamp() where singleton`); err != nil {
		t.Fatalf("drain the v4 cursor TTL: %v", err)
	}
}
