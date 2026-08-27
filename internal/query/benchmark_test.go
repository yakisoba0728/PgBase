package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const benchmarkSeedRows = 10_000

func TestBenchmarkCollectionAndInput(t *testing.T) {
	collection, input := benchmarkCollectionAndInput(20)
	if len(collection.Fields) != 20 || len(input) != 20 {
		t.Fatalf("fixture fields/input = %d/%d, want 20/20", len(collection.Fields), len(input))
	}
	statement, err := CompileInsert(
		collection,
		input,
		uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d21"),
		time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	if len(statement.Args) != 23 {
		t.Fatalf("compiled argument count = %d, want 23", len(statement.Args))
	}
	if !strings.HasPrefix(statement.SQL, `INSERT INTO "app"."benchmark_records"`) {
		t.Fatalf("compiled SQL uses unexpected path: %q", statement.SQL)
	}
}

func BenchmarkCompileInsert20Fields(b *testing.B) {
	collection, input := benchmarkCollectionAndInput(20)
	id := uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d21")
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	b.ReportAllocs()
	for range b.N {
		if _, err := CompileInsert(collection, input, id, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordServiceCRUD20Fields10000Rows(b *testing.B) {
	records, input, seededID := benchmarkRecordService(b)
	ctx := context.Background()

	b.Run("get", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.Get(ctx, "benchmark_records", seededID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("list_50", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.List(ctx, "benchmark_records", "", 50); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("create", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.Create(ctx, "benchmark_records", input); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkCollectionAndInput(fieldCount int) (schema.Collection, map[string]json.RawMessage) {
	fields := []schema.Field{
		{Name: "active", ColumnName: "active", Type: schema.TypeBoolean, Ordinal: 0},
		{Name: "priority", ColumnName: "priority", Type: schema.TypeInteger, Ordinal: 1},
		{Name: "views", ColumnName: "views", Type: schema.TypeBigInt, Ordinal: 2},
		{Name: "amount", ColumnName: "amount", Type: schema.TypeNumeric, Ordinal: 3},
		{Name: "title", ColumnName: "title", Type: schema.TypeText, Required: true, Ordinal: 4},
		{Name: "published_at", ColumnName: "published_at", Type: schema.TypeTimestampTZ, Ordinal: 5},
		{Name: "author_id", ColumnName: "author_id", Type: schema.TypeUUID, Ordinal: 6},
		{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB, Ordinal: 7},
		{Name: "enabled", ColumnName: "enabled", Type: schema.TypeBoolean, Ordinal: 8},
		{Name: "rank", ColumnName: "rank", Type: schema.TypeInteger, Ordinal: 9},
		{Name: "total", ColumnName: "total", Type: schema.TypeBigInt, Ordinal: 10},
		{Name: "price", ColumnName: "price", Type: schema.TypeNumeric, Ordinal: 11},
		{Name: "summary", ColumnName: "summary", Type: schema.TypeText, Ordinal: 12},
		{Name: "archived_at", ColumnName: "archived_at", Type: schema.TypeTimestampTZ, Ordinal: 13},
		{Name: "external_id", ColumnName: "external_id", Type: schema.TypeUUID, Ordinal: 14},
		{Name: "properties", ColumnName: "properties", Type: schema.TypeJSONB, Ordinal: 15},
		{Name: "featured", ColumnName: "featured", Type: schema.TypeBoolean, Ordinal: 16},
		{Name: "score", ColumnName: "score", Type: schema.TypeInteger, Ordinal: 17},
		{Name: "balance", ColumnName: "balance", Type: schema.TypeNumeric, Ordinal: 18},
		{Name: "notes", ColumnName: "notes", Type: schema.TypeText, Ordinal: 19},
	}
	values := map[string]json.RawMessage{
		"active":       json.RawMessage(`true`),
		"priority":     json.RawMessage(`42`),
		"views":        json.RawMessage(`"9223372036854775000"`),
		"amount":       json.RawMessage(`"1234567890.123456789"`),
		"title":        json.RawMessage(`"representative benchmark record"`),
		"published_at": json.RawMessage(`"2026-08-28T00:00:00Z"`),
		"author_id":    json.RawMessage(`"12345678-1234-4567-8901-123456789abc"`),
		"metadata":     json.RawMessage(`{"source":"benchmark","tags":["one","two"],"nested":{"count":3}}`),
		"enabled":      json.RawMessage(`false`),
		"rank":         json.RawMessage(`7`),
		"total":        json.RawMessage(`"123456789012345"`),
		"price":        json.RawMessage(`"19.9900"`),
		"summary":      json.RawMessage(`"fixed twenty-field production path"`),
		"archived_at":  json.RawMessage(`null`),
		"external_id":  json.RawMessage(`"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"`),
		"properties":   json.RawMessage(`{"visible":true,"revision":1}`),
		"featured":     json.RawMessage(`true`),
		"score":        json.RawMessage(`99`),
		"balance":      json.RawMessage(`"-1000.250"`),
		"notes":        json.RawMessage(`"deterministic baseline"`),
	}
	if fieldCount < 0 || fieldCount > len(fields) {
		panic("benchmark field count is outside the fixed catalog")
	}
	selected := append([]schema.Field(nil), fields[:fieldCount]...)
	input := make(map[string]json.RawMessage, fieldCount)
	for _, field := range selected {
		input[field.Name] = append(json.RawMessage(nil), values[field.Name]...)
	}
	return schema.Collection{
		ID:        uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d22"),
		Name:      "benchmark_records",
		TableName: "benchmark_records",
		Fields:    selected,
		CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}, input
}

func benchmarkRecordService(b *testing.B) (*Service, map[string]json.RawMessage, uuid.UUID) {
	b.Helper()
	ctx := context.Background()
	pool := benchmarkDatabase(b)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		b.Fatalf("apply benchmark migrations: %v", err)
	}
	administratorID := uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d23")
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ($1, 'benchmark@example.test', 'benchmark-only', true, $2, $2)`,
		administratorID, time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)); err != nil {
		b.Fatalf("seed benchmark administrator: %v", err)
	}

	collection, input := benchmarkCollectionAndInput(20)
	fieldInputs := make([]schema.FieldInput, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fieldInputs = append(fieldInputs, schema.FieldInput{
			Name: field.Name, Type: field.Type, Required: field.Required,
		})
	}
	definition := schema.CreateCollectionInput{Name: collection.Name, Fields: fieldInputs}
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	plan, err := schemas.PlanCreate(ctx, definition)
	if err != nil {
		b.Fatalf("plan benchmark collection: %v", err)
	}
	applied, err := schemas.ApplyCreate(ctx, definition, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		b.Fatalf("apply benchmark collection: %v", err)
	}
	seedBenchmarkRecords(b, pool, applied)

	var seededID uuid.UUID
	table := pgx.Identifier{"app", applied.TableName}.Sanitize()
	if err := pool.QueryRow(ctx, "select id from "+table+` order by created_at, id limit 1`).Scan(&seededID); err != nil {
		b.Fatalf("read seeded benchmark record: %v", err)
	}
	return NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef")), input, seededID
}

func benchmarkDatabase(b *testing.B) *database.Pool {
	b.Helper()
	adminURL := os.Getenv("PGBASE_TEST_ADMIN_URL")
	if adminURL == "" {
		b.Skip("PGBASE_TEST_ADMIN_URL is required for PostgreSQL integration benchmarks")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		b.Fatalf("connect to benchmark PostgreSQL: %v", err)
	}
	databaseName := "pgbase_bench_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "create database "+databaseIdentifier); err != nil {
		_ = admin.Close(ctx)
		b.Fatalf("create benchmark database: %v", err)
	}
	b.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, `select pg_terminate_backend(pid)
			from pg_stat_activity where datname = $1 and pid <> pg_backend_pid()`, databaseName); err != nil {
			b.Errorf("terminate benchmark database connections: %v", err)
		}
		if _, err := admin.Exec(cleanupCtx, "drop database if exists "+databaseIdentifier); err != nil {
			b.Errorf("drop benchmark database: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			b.Errorf("close benchmark admin connection: %v", err)
		}
	})
	parsed, err := url.Parse(adminURL)
	if err != nil {
		b.Fatalf("parse benchmark PostgreSQL URL: %v", err)
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	pool, err := database.Open(ctx, parsed.String(), 30*time.Second)
	if err != nil {
		b.Fatalf("open benchmark database: %v", err)
	}
	b.Cleanup(pool.Close)
	return pool
}

func seedBenchmarkRecords(b *testing.B, pool *database.Pool, collection schema.Collection) {
	b.Helper()
	columns := []string{"id", "created_at", "updated_at"}
	for _, field := range collection.Fields {
		columns = append(columns, field.ColumnName)
	}
	quotedColumns := make([]string, len(columns))
	for index, column := range columns {
		quotedColumns[index] = pgx.Identifier{column}.Sanitize()
	}
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	statement := fmt.Sprintf(`insert into %s (%s)
		select
			(substr(md5(seed::text),1,8)||'-'||substr(md5(seed::text),9,4)||'-'||substr(md5(seed::text),13,4)||'-'||substr(md5(seed::text),17,4)||'-'||substr(md5(seed::text),21,12))::uuid,
			timestamptz '2026-08-28T00:00:00Z' + seed * interval '1 microsecond',
			timestamptz '2026-08-28T00:00:00Z' + seed * interval '1 microsecond',
			seed %% 2 = 0,
			(seed %% 100)::integer,
			seed::bigint,
			seed::numeric / 100,
			'record-' || seed,
			timestamptz '2026-08-28T00:00:00Z' + seed * interval '1 second',
			'12345678-1234-4567-8901-123456789abc'::uuid,
			jsonb_build_object('source', 'benchmark', 'seed', seed),
			seed %% 3 = 0,
			(seed %% 10)::integer,
			(seed * 100)::bigint,
			seed::numeric / 10,
			'summary-' || seed,
			case when seed %% 5 = 0 then timestamptz '2026-08-28T00:00:00Z' else null end,
			(substr(md5('external-'||seed::text),1,8)||'-'||substr(md5('external-'||seed::text),9,4)||'-'||substr(md5('external-'||seed::text),13,4)||'-'||substr(md5('external-'||seed::text),17,4)||'-'||substr(md5('external-'||seed::text),21,12))::uuid,
			jsonb_build_object('visible', true, 'revision', seed),
			seed %% 7 = 0,
			(seed %% 1000)::integer,
			seed::numeric * -0.25,
			'notes-' || seed
		from generate_series(1, %d) as generated(seed)`,
		table, strings.Join(quotedColumns, ", "), benchmarkSeedRows)
	if _, err := pool.Exec(context.Background(), statement); err != nil {
		b.Fatalf("seed %d benchmark records: %v", benchmarkSeedRows, err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from "+table).Scan(&count); err != nil {
		b.Fatalf("count benchmark records: %v", err)
	}
	if count != benchmarkSeedRows {
		b.Fatalf("seeded records = %d, want %d", count, benchmarkSeedRows)
	}
}
