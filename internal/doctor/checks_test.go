package doctor

import (
	"context"
	"net/url"
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

func TestRunCompletesWithOneConnectionAvailableFromMinimumPool(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "2")
	parsed.RawQuery = query.Encode()

	pool, err := database.Open(context.Background(), parsed.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	report, err := Run(ctx, pool, migrate.New(pool, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("report = %#v, want healthy report with one connection available", report)
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

func TestRunSkipsEveryFieldOfMissingTableAndContinuesToLaterCollection(t *testing.T) {
	pool := newDoctorPool(t)
	missingID := insertCollection(t, pool, "missing", "missing")
	insertField(t, pool, missingID, "first", "text", false, false, false, 0)
	insertField(t, pool, missingID, "second", "text", false, false, false, 1)
	insertCollectionMetadata(t, pool, "later", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.later (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, title integer)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	assertFindingCode(t, report, "collection_table_missing")
	assertFindingCode(t, report, "collection_column_type_mismatch")
}

func TestRunReportsUnexpectedPhysicalColumnType(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, title integer)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_column_type_mismatch")
}

func TestRunReportsMissingCustomColumnAndContinuesToLaterCollection(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null)`); err != nil {
		t.Fatal(err)
	}
	insertCollectionMetadata(t, pool, "later", "body", "text")
	if _, err := pool.Exec(context.Background(), `create table app.later (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, body integer)`); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, pool)
	assertFindingCode(t, report, "collection_column_missing")
	assertFindingCode(t, report, "collection_column_type_mismatch")
}

func TestRunReportsStaleSchemaState(t *testing.T) {
	pool := newDoctorPool(t)
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.schema_state set version = 1 where singleton"); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "schema_state_stale")
}

func TestRunRejectsMisleadingAndMalformedIndexes(t *testing.T) {
	cases := []struct {
		name   string
		index  string
		unique bool
	}{
		{name: "substring column", index: `create index misleading_username on app.posts (username)`},
		{name: "composite index", index: `create index misleading_composite on app.posts (name, username)`},
		{name: "wrong column order", index: `create index misleading_order on app.posts (username, name)`},
		{name: "expression index", index: `create index misleading_expression on app.posts (lower(name))`},
		{name: "partial index", index: `create index misleading_partial on app.posts (name) where name is not null`},
		{name: "nonunique for unique metadata", index: `create index misleading_nonunique on app.posts (name)`, unique: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			collectionID := insertCollection(t, pool, "posts", "posts")
			insertField(t, pool, collectionID, "name", "text", false, tc.unique, !tc.unique, 0)
			if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, name text, username text)`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), tc.index); err != nil {
				t.Fatal(err)
			}
			assertFindingCode(t, runDoctor(t, pool), "collection_index_missing")
		})
	}
}

func TestRunAcceptsExactSingleColumnUniqueIndex(t *testing.T) {
	pool := newDoctorPool(t)
	collectionID := insertCollection(t, pool, "posts", "posts")
	insertField(t, pool, collectionID, "name", "text", false, true, false, 0)
	if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null, name text);
		create unique index posts_name_unique on app.posts (name)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want exact index to be healthy", report)
	}
}

func TestRunReportsUnreachableDatabase(t *testing.T) {
	report, err := Run(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("unreachable database did not return an error")
	}
	assertFindingCode(t, report, "database_unreachable")
}

func TestRunReportsClosedDatabasePoolAsUnreachable(t *testing.T) {
	pool := newDoctorPool(t)
	pool.Close()
	report, err := Run(context.Background(), pool, migrate.New(pool, nil))
	if err == nil {
		t.Fatal("closed database pool did not return an error")
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
	collectionID := insertCollection(t, pool, collection, collection)
	insertField(t, pool, collectionID, field, fieldType, false, false, false, 0)
}

func insertCollection(t *testing.T, pool *database.Pool, collection, table string) uuid.UUID {
	t.Helper()
	collectionID := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collections (id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $3, 'active', current_timestamp, current_timestamp)`, collectionID, collection, table); err != nil {
		t.Fatal(err)
	}
	return collectionID
}

func insertField(t *testing.T, pool *database.Pool, collectionID uuid.UUID, field, fieldType string, required, unique, indexed bool, ordinal int) {
	t.Helper()
	fieldID := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.fields (id, collection_id, name, column_name, field_type, required, is_unique, is_indexed, has_default, ordinal, created_at)
		values ($1, $2, $3, $3, $4, $5, $6, $7, false, $8, current_timestamp)`, fieldID, collectionID, field, fieldType, required, unique, indexed, ordinal); err != nil {
		t.Fatal(err)
	}
}
