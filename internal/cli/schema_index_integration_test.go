//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func readSchemaIndexStatus(t *testing.T, databaseURL string) schemaIndexReport {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"schema-index", "status", "--json"},
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("status exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report schemaIndexReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("status report=%q err=%v", stdout.String(), err)
	}
	return report
}

// The whole operator surface, end to end: report the closed gate, refuse to
// activate without a proven fleet, then build and turn it off again.
func TestSchemaIndexDrivesTheCompanionRolloutFromTheCommandLine(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	prepareDoctorDatabase(t, databaseURL)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	seedSortableCollection(t, pool)

	initial := readSchemaIndexStatus(t, databaseURL)
	if !initial.Installed || initial.State != "pending" || initial.Expected != 1 ||
		initial.Missing != 1 || len(initial.Writers) != 0 {
		t.Fatalf("initial status = %#v", initial)
	}

	// No instance has proven its compiler version, so there is no fleet to
	// activate against and the command must refuse rather than open the gate.
	var stdout, stderr bytes.Buffer
	if code := Execute(ctx, []string{"schema-index", "activate"},
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr}); code != 1 {
		t.Fatalf("unproven activation exit=%d stdout=%q", code, stdout.String())
	}
	if got := readSchemaIndexStatus(t, databaseURL); got.State != "pending" {
		t.Fatalf("refused activation moved the capability: %#v", got)
	}

	// A live writer at this binary's compiler version is what an operator
	// confirms as the witness.
	instanceID := "0199c300-0000-7000-8000-000000000001"
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
		values ($1, 2, transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + interval '90 seconds')`, instanceID); err != nil {
		t.Fatal(err)
	}
	witnessed := readSchemaIndexStatus(t, databaseURL)
	if len(witnessed.Writers) != 1 || witnessed.Writers[0].InstanceID != instanceID ||
		witnessed.Writers[0].CompilerVersion != 2 {
		t.Fatalf("status did not report the live writer: %#v", witnessed)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute(ctx, []string{"schema-index", "activate", "--witness", instanceID},
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("activate exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "state=building") {
		t.Fatalf("activate output = %q", stdout.String())
	}

	stdout.Reset()
	if code := Execute(ctx, []string{"schema-index", "build"},
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("build exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "state=ready") || !strings.Contains(stdout.String(), "indexes=1") {
		t.Fatalf("build output = %q", stdout.String())
	}
	built := readSchemaIndexStatus(t, databaseURL)
	if built.State != "ready" || built.Valid != 1 || built.Missing != 0 || built.Invalid != 0 {
		t.Fatalf("built status = %#v", built)
	}

	stdout.Reset()
	if code := Execute(ctx, []string{"schema-index", "rollback"},
		migrationEnv(databaseURL), IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("rollback exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	rolledBack := readSchemaIndexStatus(t, databaseURL)
	// The gate is off but the object stays, so a forward fix does not rebuild it.
	if rolledBack.State != "pending" || rolledBack.Valid != 1 {
		t.Fatalf("rolled back status = %#v", rolledBack)
	}
}

func seedSortableCollection(t *testing.T, pool *database.Pool) {
	t.Helper()
	const collectionID = "0199c300-0000-7000-8000-000000000010"
	const fieldID = "0199c300-0000-7000-8000-000000000011"
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ('`+collectionID+`', 'sortable', 'sortable', 'active',
			current_timestamp, current_timestamp);
	insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at)
		values ('`+collectionID+`', 0, 0, transaction_timestamp());
	insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at)
		values ('`+collectionID+`', 0, 'off', transaction_timestamp());
	insert into pgbase_meta.fields
		(id, collection_id, name, column_name, field_type, required, is_unique,
		 is_indexed, has_default, ordinal, created_at)
		values ('`+fieldID+`', '`+collectionID+`', 'nickname', 'nickname', 'text',
			false, false, true, false, 0, current_timestamp);
	create table app.sortable (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		nickname text
	);
	create index sortable_nickname_idx on app.sortable (nickname)`); err != nil {
		t.Fatal(err)
	}
}
