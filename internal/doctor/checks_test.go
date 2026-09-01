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
