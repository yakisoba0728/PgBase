//go:build integration

package system_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

const insertRelationMetadataSQL = `INSERT INTO pgbase_meta.field_relations
	(field_id, source_collection_id, target_collection_id, cardinality,
	 min_select, max_select, on_delete, value_codec_version, storage_kind,
	 storage_name, created_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, transaction_timestamp())`

func TestRelationSchemaMigrationUpgradesEmptyDatabase(t *testing.T) {
	ctx, pool := newRelationSchema(t)

	var relationSchema, metadataTable, sourceIndex, incomingIndex bool
	if err := pool.QueryRow(ctx, `SELECT
		to_regnamespace('pgbase_relation') IS NOT NULL,
		to_regclass('pgbase_meta.field_relations') IS NOT NULL,
		to_regclass('pgbase_meta.field_relations_source_collection_idx') IS NOT NULL,
		to_regclass('pgbase_meta.field_relations_incoming_collection_idx') IS NOT NULL`).Scan(
		&relationSchema,
		&metadataTable,
		&sourceIndex,
		&incomingIndex,
	); err != nil {
		t.Fatal(err)
	}
	if !relationSchema || !metadataTable || !sourceIndex || !incomingIndex {
		t.Fatalf("relation objects schema=%t table=%t source_index=%t incoming_index=%t",
			relationSchema, metadataTable, sourceIndex, incomingIndex)
	}

	insertRelationCollections(t, ctx, pool)
	insertRelationField(t, ctx, pool,
		"019551d0-e9c0-7000-8000-000000000111",
		"019551d0-e9c0-7000-8000-000000000100",
		"owner", 0,
	)
	if _, err := pool.Exec(ctx, insertRelationMetadataSQL,
		"019551d0-e9c0-7000-8000-000000000111",
		"019551d0-e9c0-7000-8000-000000000100",
		"019551d0-e9c0-7000-8000-000000000300",
		"single", 0, 1, "set_null", 1, "inline_fk_v1", "rel_owner_111",
	); err != nil {
		t.Fatalf("insert relation metadata after empty upgrade: %v", err)
	}
}

func TestRelationSchemaMetadataConstraints(t *testing.T) {
	ctx, pool := newRelationSchema(t)
	insertRelationCollections(t, ctx, pool)

	type relationCase struct {
		name         string
		source       string
		target       string
		cardinality  string
		minimum      int
		maximum      int
		onDelete     string
		codecVersion int
		storageKind  string
		storageName  string
		wantSQLState string
	}
	cases := []relationCase{
		{
			name: "optional single", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "set_null",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "valid_single",
		},
		{
			name: "bounded multiple", source: relationSourceID, target: relationTargetID,
			cardinality: "multiple", minimum: 1, maximum: 10, onDelete: "cascade",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "valid_multiple",
		},
		{
			name: "source collection must own field", source: relationOtherSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "wrong_source",
			wantSQLState: "23503",
		},
		{
			name: "target must exist", source: relationSourceID,
			target:      "019551d0-e9c0-7000-8000-000000000999",
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "missing_target",
			wantSQLState: "23503",
		},
		{
			name: "unknown cardinality", source: relationSourceID, target: relationTargetID,
			cardinality: "many", minimum: 0, maximum: 2, onDelete: "restrict",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "unknown_cardinality",
			wantSQLState: "23514",
		},
		{
			name: "single maximum is one", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 2, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "single_max_two",
			wantSQLState: "23514",
		},
		{
			name: "single uses inline storage", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "single_edge",
			wantSQLState: "23514",
		},
		{
			name: "multiple maximum starts at two", source: relationSourceID, target: relationTargetID,
			cardinality: "multiple", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "multiple_max_one",
			wantSQLState: "23514",
		},
		{
			name: "multiple minimum cannot exceed maximum", source: relationSourceID, target: relationTargetID,
			cardinality: "multiple", minimum: 3, maximum: 2, onDelete: "restrict",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "multiple_reverse_bounds",
			wantSQLState: "23514",
		},
		{
			name: "multiple uses edge storage", source: relationSourceID, target: relationTargetID,
			cardinality: "multiple", minimum: 0, maximum: 2, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "multiple_inline",
			wantSQLState: "23514",
		},
		{
			name: "set null requires zero minimum", source: relationSourceID, target: relationTargetID,
			cardinality: "multiple", minimum: 1, maximum: 2, onDelete: "set_null",
			codecVersion: 1, storageKind: "edge_table_v1", storageName: "required_set_null",
			wantSQLState: "23514",
		},
		{
			name: "codec version is pinned", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 2, storageKind: "inline_fk_v1", storageName: "codec_two",
			wantSQLState: "23514",
		},
		{
			name: "storage name is nonempty", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "",
			wantSQLState: "23514",
		},
		{
			name: "storage name is at most 63 bytes", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: strings.Repeat("한", 22),
			wantSQLState: "23514",
		},
		{
			name: "storage name is a bare identifier", source: relationSourceID, target: relationTargetID,
			cardinality: "single", minimum: 0, maximum: 1, onDelete: "restrict",
			codecVersion: 1, storageKind: "inline_fk_v1", storageName: "invalid storage name",
			wantSQLState: "23514",
		},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fieldID := relationFieldID(index)
			insertRelationField(t, ctx, pool, fieldID, relationSourceID, "relation_field_"+storageSafeName(testCase.name), index)
			_, err := pool.Exec(ctx, insertRelationMetadataSQL,
				fieldID,
				testCase.source,
				testCase.target,
				testCase.cardinality,
				testCase.minimum,
				testCase.maximum,
				testCase.onDelete,
				testCase.codecVersion,
				testCase.storageKind,
				testCase.storageName,
			)
			if testCase.wantSQLState == "" {
				if err != nil {
					t.Fatalf("insert valid metadata: %v", err)
				}
				return
			}
			requirePostgresErrorCode(t, err, testCase.wantSQLState)
		})
	}

	duplicateFieldID := relationFieldID(len(cases))
	insertRelationField(t, ctx, pool, duplicateFieldID, relationSourceID, "duplicate_storage", len(cases))
	_, err := pool.Exec(ctx, insertRelationMetadataSQL,
		duplicateFieldID, relationSourceID, relationTargetID,
		"single", 0, 1, "restrict", 1, "inline_fk_v1", "valid_single",
	)
	requirePostgresErrorCode(t, err, "23505")

	crossSourceFieldID := relationFieldID(len(cases) + 1)
	insertRelationField(t, ctx, pool, crossSourceFieldID, relationOtherSourceID, "same_local_storage", len(cases)+1)
	if _, err := pool.Exec(ctx, insertRelationMetadataSQL,
		crossSourceFieldID, relationOtherSourceID, relationTargetID,
		"single", 0, 1, "restrict", 1, "inline_fk_v1", "valid_single",
	); err != nil {
		t.Fatalf("reuse collection-local storage name: %v", err)
	}
}

func TestRelationSchemaMetadataSourceAndTargetDeleteBehavior(t *testing.T) {
	ctx, pool := newRelationSchema(t)
	insertRelationCollections(t, ctx, pool)

	const firstFieldID = "019551d0-e9c0-7000-8000-000000000191"
	insertRelationField(t, ctx, pool, firstFieldID, relationSourceID, "first_relation", 0)
	if _, err := pool.Exec(ctx, insertRelationMetadataSQL,
		firstFieldID, relationSourceID, relationTargetID,
		"single", 0, 1, "restrict", 1, "inline_fk_v1", "delete_behavior_one",
	); err != nil {
		t.Fatal(err)
	}

	_, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.collections WHERE id = $1`, relationTargetID)
	// PostgreSQL 18 reports ON DELETE RESTRICT with the SQL-standard 23001;
	// supported PostgreSQL 16 and 17 report the same protected FK as 23503.
	requirePostgresErrorCodeOneOf(t, err, "23001", "23503")

	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.collections WHERE id = $1`, relationSourceID); err != nil {
		t.Fatalf("delete source collection: %v", err)
	}
	assertRelationMetadataCount(t, ctx, pool, firstFieldID, 0)
	var targetCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pgbase_meta.collections WHERE id = $1`, relationTargetID,
	).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 {
		t.Fatalf("target collection count=%d want=1", targetCount)
	}

	const secondFieldID = "019551d0-e9c0-7000-8000-000000000192"
	insertRelationField(t, ctx, pool, secondFieldID, relationOtherSourceID, "second_relation", 0)
	if _, err := pool.Exec(ctx, insertRelationMetadataSQL,
		secondFieldID, relationOtherSourceID, relationTargetID,
		"multiple", 0, 20, "cascade", 1, "edge_table_v1", "delete_behavior_two",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.fields WHERE id = $1`, secondFieldID); err != nil {
		t.Fatalf("delete source field: %v", err)
	}
	assertRelationMetadataCount(t, ctx, pool, secondFieldID, 0)
}

func TestRelationSchemaSelfRelationDoesNotPreventSourceCollectionDelete(t *testing.T) {
	ctx, pool := newRelationSchema(t)
	insertRelationCollections(t, ctx, pool)

	const selfFieldID = "019551d0-e9c0-7000-8000-000000000193"
	insertRelationField(t, ctx, pool, selfFieldID, relationSourceID, "parent", 0)
	if _, err := pool.Exec(ctx, insertRelationMetadataSQL,
		selfFieldID, relationSourceID, relationSourceID,
		"single", 0, 1, "set_null", 1, "inline_fk_v1", "parent",
	); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM pgbase_meta.collections WHERE id = $1`, relationSourceID); err != nil {
		t.Fatalf("delete source collection with self relation: %v", err)
	}
	assertRelationMetadataCount(t, ctx, pool, selfFieldID, 0)
}

const (
	relationSourceID      = "019551d0-e9c0-7000-8000-000000000100"
	relationOtherSourceID = "019551d0-e9c0-7000-8000-000000000200"
	relationTargetID      = "019551d0-e9c0-7000-8000-000000000300"
)

func newRelationSchema(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range system.Migrations() {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
	return ctx, pool
}

func insertRelationCollections(t *testing.T, ctx context.Context, pool *database.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
	VALUES
		($1, 'relation_source', 'relation_source', 'active', transaction_timestamp(), transaction_timestamp()),
		($2, 'relation_other_source', 'relation_other_source', 'active', transaction_timestamp(), transaction_timestamp()),
		($3, 'relation_target', 'relation_target', 'active', transaction_timestamp(), transaction_timestamp())`,
		relationSourceID, relationOtherSourceID, relationTargetID,
	)
	if err != nil {
		t.Fatalf("insert relation collections: %v", err)
	}
}

func insertRelationField(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	fieldID string,
	collectionID string,
	name string,
	ordinal int,
) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.fields
		(id, collection_id, name, column_name, field_type, required, is_unique,
		 is_indexed, has_default, default_json, ordinal, created_at)
	VALUES ($1, $2, $3, $3, 'relation', false, false, false, false, NULL, $4,
		transaction_timestamp())`, fieldID, collectionID, name, ordinal)
	if err != nil {
		t.Fatalf("insert relation field: %v", err)
	}
}

func relationFieldID(index int) string {
	return "019551d0-e9c0-7000-8000-" + leftPadDecimal(index+1000, 12)
}

func leftPadDecimal(value int, width int) string {
	result := strings.Repeat("0", width) + strconv.Itoa(value)
	return result[len(result)-width:]
}

func storageSafeName(value string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(value)
}

func assertRelationMetadataCount(t *testing.T, ctx context.Context, pool *database.Pool, fieldID string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pgbase_meta.field_relations WHERE field_id = $1`, fieldID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("relation metadata count for field %s=%d want=%d", fieldID, count, want)
	}
}

func requirePostgresErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		got := ""
		if postgresError != nil {
			got = postgresError.Code
		}
		t.Fatalf("error=%v SQLSTATE=%q want=%q", err, got, want)
	}
}

func requirePostgresErrorCodeOneOf(t *testing.T, err error, want ...string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		for _, code := range want {
			if postgresError.Code == code {
				return
			}
		}
	}
	got := ""
	if postgresError != nil {
		got = postgresError.Code
	}
	t.Fatalf("error=%v SQLSTATE=%q want one of %v", err, got, want)
}
