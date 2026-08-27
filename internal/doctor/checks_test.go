package doctor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRunReportsHealthyMigratedDatabase(t *testing.T) {
	pool := newDoctorPool(t)
	report, err := Run(context.Background(), pool, migrate.New(pool, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("report = %#v, want healthy report", report)
	}
}

func TestRunReportsMigrationChecksumMismatch(t *testing.T) {
	pool := newDoctorPool(t)
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.system_migrations set checksum = decode(repeat('00', 32), 'hex') where version = 1"); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "migration_checksum_mismatch")
}

func TestRunReportsMissingPhysicalCollectionTable(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	assertFindingCode(t, runDoctor(t, pool), "collection_table_missing")
}

func TestRunReportsUnexpectedPhysicalColumnType(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, title integer)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_column_type_mismatch")
}

func TestRunReportsStaleSchemaState(t *testing.T) {
	pool := newDoctorPool(t)
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.schema_state set version = 1 where singleton"); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "schema_state_stale")
}

func TestRunReportsUnreachableDatabase(t *testing.T) {
	report, err := Run(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("unreachable database did not return an error")
	}
	assertFindingCode(t, report, "database_unreachable")
}

func newDoctorPool(t *testing.T) *database.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func runDoctor(t *testing.T, pool *database.Pool) Report {
	t.Helper()
	report, err := Run(context.Background(), pool, migrate.New(pool, nil))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertFindingCode(t *testing.T, report Report, want string) {
	t.Helper()
	if report.OK {
		t.Fatalf("report = %#v, want finding %q", report, want)
	}
	for _, finding := range report.Findings {
		if finding.Code == want {
			return
		}
	}
	t.Fatalf("findings = %#v, want code %q", report.Findings, want)
}

func insertCollectionMetadata(t *testing.T, pool *database.Pool, collection, field, fieldType string) {
	t.Helper()
	collectionID := uuid.New()
	fieldID := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collections (id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $2, 'active', current_timestamp, current_timestamp)`, collectionID, collection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.fields (id, collection_id, name, column_name, field_type, required, is_unique, is_indexed, has_default, ordinal, created_at)
		values ($1, $2, $3, $3, $4, false, false, false, false, 0, current_timestamp)`, fieldID, collectionID, field, fieldType); err != nil {
		t.Fatal(err)
	}
}
