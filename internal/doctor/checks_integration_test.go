//go:build integration

package doctor

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
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

func TestRunRequiresTheSchemaOperationsFeatureHistory(t *testing.T) {
	migrations := system.Migrations()
	if len(migrations) != 6 {
		t.Fatalf("schema-operations feature manifest = %#v", migrations)
	}
	tests := []struct {
		name     string
		through  int
		dirty    bool
		wantCode string
	}{
		{name: "v4 rejected", through: 4, wantCode: "migration_not_current"},
		{name: "v5 rejected", through: 5, wantCode: "migration_not_current"},
		{name: "v6 accepted", through: 6},
		{name: "dirty v6 rejected", through: 6, dirty: true, wantCode: "migration_checksum_mismatch"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := testutil.NewDatabase(t)
			testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:testCase.through])
			pool, err := database.Open(context.Background(), databaseURL, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if testCase.dirty {
				if _, err := pool.Exec(context.Background(), `update pgbase_meta.system_migrations
					set checksum = decode(repeat('00', 32), 'hex') where version = 1`); err != nil {
					t.Fatal(err)
				}
			}
			report, err := Run(context.Background(), pool, migrate.New(pool, nil))
			if err != nil {
				t.Fatal(err)
			}
			if testCase.wantCode == "" {
				if !report.OK || len(report.Findings) != 0 {
					t.Fatalf("feature report = %#v, want healthy", report)
				}
				return
			}
			assertFindingCode(t, report, testCase.wantCode)
		})
	}
}

func TestRunReportsBoundedCatalogReductionFindingWhenRolloutPreflightExceedsLimit(t *testing.T) {
	pool := newDoctorPool(t)
	const secretCatalogFragment = "legacy-catalog-secret-must-not-leak"
	collectionID := insertCollection(t, pool, "legacy_catalog_limit", "legacy_catalog_limit")
	legacyDefault, err := json.Marshal(secretCatalogFragment + strings.Repeat("x", (16<<20)+1))
	if err != nil {
		t.Fatal(err)
	}
	insertFieldWithDefault(t, pool, collectionID, "body", "text", string(legacyDefault), 0)
	report, err := Run(context.Background(), pool, migrate.New(pool, nil))
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || len(report.Findings) != 1 {
		t.Fatalf("report = %#v, want one blocking finding", report)
	}
	finding := report.Findings[0]
	if finding.Code != "catalog_limit_exceeded" || finding.Message != "Schema catalog exceeds 16 MiB; remove unused fields or collections during maintenance, then rerun pgbase doctor" {
		t.Fatalf("finding = %#v", finding)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 256 {
		t.Fatalf("doctor finding grew beyond its bounded diagnostic: %d bytes", len(encoded))
	}
	if strings.Contains(string(encoded), secretCatalogFragment) {
		t.Fatalf("doctor finding leaked catalog/error content: %s", encoded)
	}
}

func TestRunFailsClosedWhenRolloutPreflightFindsInvalidCatalog(t *testing.T) {
	pool := newDoctorPool(t)
	const secretCatalogFragment = "invalid_catalog_secret_must_not_leak"
	ctx := context.Background()
	input := schema.CreateCollectionInput{
		Name: secretCatalogFragment,
		Fields: []schema.FieldInput{{
			Name: "title",
			Type: schema.TypeText,
		}},
	}
	plan, err := (schema.Planner{}).PlanCreateWithCatalog(0, schema.Catalog{Collections: map[string]schema.Collection{}}, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range plan.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	createdAt := time.Now().UTC()
	created := schema.Collection{
		ID:        uuid.New(),
		Name:      secretCatalogFragment,
		TableName: secretCatalogFragment,
		Fields: []schema.Field{{
			ID:         uuid.New(),
			Name:       "title",
			ColumnName: "title",
			Type:       schema.TypeText,
			Ordinal:    0,
		}},
		CreatedAt: createdAt,
	}
	if err := schema.NewStore(pool).InsertCollection(ctx, created, plan.Definition, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.fields set ordinal = 1 where collection_id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}

	report, err := Run(ctx, pool, migrate.New(pool, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertFindingCode(t, report, "catalog_invalid")
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretCatalogFragment) {
		t.Fatalf("doctor finding leaked catalog content: %s", encoded)
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

func TestRunReportsExactMigrationHistoryIssue(t *testing.T) {
	tests := []struct {
		name     string
		breakSQL string
		wantCode string
	}{
		{
			name: "unknown version",
			breakSQL: `insert into pgbase_meta.system_migrations
				(version, name, checksum, execution_ms)
				values (99, 'future', decode(repeat('00', 32), 'hex'), 0)`,
			wantCode: "migration_unknown_version",
		},
		{
			name:     "history gap",
			breakSQL: "delete from pgbase_meta.system_migrations where version = 2",
			wantCode: "migration_history_gap",
		},
		{
			name: "duplicate version",
			breakSQL: `alter table pgbase_meta.system_migrations drop constraint system_migrations_pkey;
				insert into pgbase_meta.system_migrations
					(version, name, checksum, applied_at, execution_ms)
				select version, name, checksum, applied_at, execution_ms
				from pgbase_meta.system_migrations where version = 1`,
			wantCode: "migration_history_duplicate",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			if _, err := pool.Exec(context.Background(), testCase.breakSQL); err != nil {
				t.Fatal(err)
			}
			assertFindingCode(t, runDoctor(t, pool), testCase.wantCode)
		})
	}
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
	);
		create index defaults_page on app.defaults (created_at, id)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want matching typed defaults to be healthy", report)
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

func TestRunReportsNullableBaseTimestampColumns(t *testing.T) {
	for _, column := range []string{"created_at", "updated_at"} {
		t.Run(column, func(t *testing.T) {
			pool := newDoctorPool(t)
			insertCollection(t, pool, "posts", "posts")
			nullableCreated := column == "created_at"
			nullableUpdated := column == "updated_at"
			createdNullability := "not null"
			if nullableCreated {
				createdNullability = ""
			}
			updatedNullability := "not null"
			if nullableUpdated {
				updatedNullability = ""
			}
			if _, err := pool.Exec(context.Background(), `create table app.posts (
				id uuid primary key,
				created_at timestamptz `+createdNullability+`,
				updated_at timestamptz `+updatedNullability+`
			);
				create index posts_page on app.posts (created_at, id)`); err != nil {
				t.Fatal(err)
			}
			assertFindingCode(t, runDoctor(t, pool), "collection_column_nullability_mismatch")
		})
	}
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
	);
		create index posts_page on app.posts (created_at, id)`); err != nil {
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
		{name: "wrong access method", index: `create index misleading_hash on app.posts using hash (name)`},
		{name: "descending", index: `create index misleading_desc on app.posts (name desc)`},
		{name: "nulls first", index: `create index misleading_nulls on app.posts (name nulls first)`},
		{name: "nondefault operator class", index: `create index misleading_opclass on app.posts (name text_pattern_ops)`},
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
		create index posts_page on app.posts (created_at, id);
		create unique index posts_name_unique on app.posts (name)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want exact index to be healthy", report)
	}
}

func TestRunReportsMissingOrMismatchedCollectionPageIndex(t *testing.T) {
	// The planner emits a (created_at, id) index for every collection and the
	// list kernel assumes it exists, so doctor has to notice when it is gone or
	// replaced by an index PostgreSQL cannot use for the same ordering.
	cases := []struct {
		name  string
		index string
	}{
		{name: "missing"},
		{name: "reversed column order", index: `create index posts_page on app.posts (id, created_at)`},
		{name: "created_at only", index: `create index posts_page on app.posts (created_at)`},
		{name: "extra leading column", index: `create index posts_page on app.posts (updated_at, created_at, id)`},
		{name: "partial", index: `create index posts_page on app.posts (created_at, id) where created_at > '2000-01-01T00:00:00Z'::timestamptz`},
		{name: "wrong access method", index: `create index posts_page on app.posts using brin (created_at, id)`},
		{name: "descending", index: `create index posts_page on app.posts (created_at desc, id)`},
		{name: "nulls first", index: `create index posts_page on app.posts (created_at nulls first, id)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newDoctorPool(t)
			insertCollection(t, pool, "posts", "posts")
			if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null)`); err != nil {
				t.Fatal(err)
			}
			if tc.index != "" {
				if _, err := pool.Exec(context.Background(), tc.index); err != nil {
					t.Fatal(err)
				}
			}
			assertFindingCode(t, runDoctor(t, pool), "collection_page_index_missing")
		})
	}
}

func TestRunAcceptsPlannerGeneratedCollectionPageIndex(t *testing.T) {
	pool := newDoctorPool(t)
	insertCollection(t, pool, "posts", "posts")
	if _, err := pool.Exec(context.Background(), `create table app.posts (id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null);
		create index posts_page on app.posts (created_at, id)`); err != nil {
		t.Fatal(err)
	}
	report := runDoctor(t, pool)
	if !report.OK {
		t.Fatalf("report = %#v, want the pagination index to be healthy", report)
	}
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
