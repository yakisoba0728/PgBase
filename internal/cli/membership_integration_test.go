//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	// Byte-identical to the history the previous release ran on, then populated
	// the way that release would have populated it.
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:5])
	seedPopulatedDeployment(t, databaseURL)

	var stdout, stderr bytes.Buffer
	if code := Execute(ctx, []string{"migrate", "up"}, migrationEnv(databaseURL),
		IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("migrate up exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

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
