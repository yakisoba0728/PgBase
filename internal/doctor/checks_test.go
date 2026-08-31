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

func TestRunReportsUncataloguedPhysicalCollectionTable(t *testing.T) {
	pool := newDoctorPool(t)
	if _, err := pool.Exec(context.Background(), `create table app.stray (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_table_orphaned")
}

func TestRunReportsUncataloguedZeroColumnPhysicalTable(t *testing.T) {
	pool := newDoctorPool(t)
	if _, err := pool.Exec(context.Background(), `create table app.empty_stray ()`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_table_orphaned")
}

func TestRunReportsUncataloguedPhysicalCollectionColumn(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.posts (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		title text,
		legacy text
	)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_column_orphaned")
}

func TestRunReportsUnexpectedPhysicalColumnDefault(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollectionMetadata(t, pool, "posts", "title", "text")
	if _, err := pool.Exec(context.Background(), `create table app.posts (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		title text default 'physical-only'
	)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_column_default_mismatch")
}

func TestRunReportsChangedPhysicalColumnDefaultValue(t *testing.T) {
	pool := newDoctorPool(t)
	collectionID := insertCollection(t, pool, "posts", "posts")
	insertFieldWithDefault(t, pool, collectionID, "title", "text", `"catalog-right"`, 0)
	if _, err := pool.Exec(context.Background(), `create table app.posts (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		title text default 'physical-wrong'
	)`); err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, runDoctor(t, pool), "collection_column_default_mismatch")
}

func TestRunAcceptsMatchingTypedPhysicalDefaults(t *testing.T) {
	pool := newDoctorPool(t)
	collectionID := insertCollection(t, pool, "defaults", "defaults")
	defaults := []struct {
		name, fieldType, value string
	}{
		{name: "flag", fieldType: "boolean", value: `false`},
		{name: "count", fieldType: "integer", value: `"1"`},
		{name: "total", fieldType: "bigint", value: `"-42"`},
		{name: "ratio", fieldType: "numeric", value: `"3.4"`},
		{name: "label", fieldType: "text", value: `"sa\\fe'x"`},
		{name: "published_at", fieldType: "timestamptz", value: `{"kind":"now"}`},
		{name: "fixed_at", fieldType: "timestamptz", value: `"2026-08-28T12:34:56.123456789Z"`},
		{name: "tie_even_at", fieldType: "timestamptz", value: `"2026-08-28T12:34:56.1234565Z"`},
		{name: "external_id", fieldType: "uuid", value: `"12345678-1234-4567-8901-123456789abc"`},
		{name: "metadata", fieldType: "jsonb", value: `{"z":1,"a":[2]}`},
	}
	for ordinal, field := range defaults {
		insertFieldWithDefault(t, pool, collectionID, field.name, field.fieldType, field.value, ordinal)
	}
	if _, err := pool.Exec(context.Background(), `create table app.defaults (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null,
		flag boolean default false,
		count integer default 1,
		total bigint default -42,
		ratio numeric default 3.4::numeric,
		label text default E'sa\\fe''x'::text,
		published_at timestamptz default current_timestamp,
		fixed_at timestamptz default E'2026-08-28T12:34:56.123456789Z'::timestamptz,
		tie_even_at timestamptz default E'2026-08-28T12:34:56.1234565Z'::timestamptz,
		external_id uuid default E'12345678-1234-4567-8901-123456789abc'::uuid,
		metadata jsonb default E'{"a":[2],"z":1}'::jsonb
	)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want matching typed defaults to be healthy", report)
	}
}

func TestPostgresTimestampPrecisionRoundsHalfToEven(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "2026-08-28T12:34:56.123456499Z", want: "2026-08-28T12:34:56.123456Z"},
		{input: "2026-08-28T12:34:56.1234565Z", want: "2026-08-28T12:34:56.123456Z"},
		{input: "2026-08-28T12:34:56.1234575Z", want: "2026-08-28T12:34:56.123458Z"},
		{input: "2026-08-28T12:34:56.123456501Z", want: "2026-08-28T12:34:56.123457Z"},
		{input: "2026-08-28T12:34:56.9999995Z", want: "2026-08-28T12:34:57Z"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			input, err := time.Parse(time.RFC3339Nano, tt.input)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, tt.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := postgresTimestampPrecision(input); !got.Equal(want) {
				t.Fatalf("postgresTimestampPrecision(%s) = %s, want %s", tt.input, got.Format(time.RFC3339Nano), tt.want)
			}
		})
	}
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

func TestRunAcceptsExactSingleIDPrimaryKey(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollection(t, pool, "posts", "posts")
	if _, err := pool.Exec(context.Background(), `create table app.posts (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want exact single-column id primary key to be healthy", report)
	}
}

func TestRunRejectsPrimaryKeyOtherThanExactSingleIDColumn(t *testing.T) {
	tests := []struct {
		name       string
		definition string
	}{
		{
			name: "different column",
			definition: `create table app.posts (
				id uuid not null,
				discriminator text primary key,
				created_at timestamptz not null,
				updated_at timestamptz not null
			)`,
		},
		{
			name: "composite including id",
			definition: `create table app.posts (
				id uuid not null,
				discriminator text not null,
				created_at timestamptz not null,
				updated_at timestamptz not null,
				primary key (id, discriminator)
			)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			insertCollection(t, pool, "posts", "posts")
			if _, err := pool.Exec(context.Background(), tt.definition); err != nil {
				t.Fatal(err)
			}

			assertFindingCode(t, runDoctor(t, pool), "collection_primary_key_missing")
		})
	}
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

func insertFieldWithDefault(t *testing.T, pool *database.Pool, collectionID uuid.UUID, field, fieldType, defaultJSON string, ordinal int) {
	t.Helper()
	fieldID := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.fields
		(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed, has_default, default_json, ordinal, created_at)
		values ($1, $2, $3, $3, $4, false, false, false, true, $5::jsonb, $6, current_timestamp)`,
		fieldID, collectionID, field, fieldType, defaultJSON, ordinal); err != nil {
		t.Fatal(err)
	}
}
