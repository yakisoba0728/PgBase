package system_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestSQLConsoleMigrationStoresOnlyRedactedExecutionAndReceipt(t *testing.T) {
	ctx := context.Background()
	migrations := system.Migrations()
	if len(migrations) != 41 || migrations[40].Version != 41 || migrations[40].Name != "sql_console" {
		t.Fatalf("last migration = %#v, want 41 sql_console", migrations[len(migrations)-1])
	}

	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}

	var columns string
	if err := pool.QueryRow(ctx, `select string_agg(column_name, ',' order by ordinal_position)
		from information_schema.columns
		where table_schema='pgbase_meta' and table_name='sql_console_executions'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw_sql", "parameters", "result", "session_key", "session_digest"} {
		for _, column := range strings.Split(columns, ",") {
			if column == forbidden {
				t.Fatalf("durable execution log contains forbidden column %q in %q", column, columns)
			}
		}
	}

	const actor = "45e4af68-3d25-4c53-ab68-09ca82f4af25"
	const execution = "0e6a6d6a-936c-4b52-a631-f89f56fe74d5"
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id,email,password_hash,active,created_at,updated_at)
		values ($1,'n8@example.test','hash',true,clock_timestamp(),clock_timestamp())`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.sql_console_executions
		(execution_id,administrator_id,mode,dry_run,idempotency_hash,request_hash,query_fingerprint,
		 status,statement_count,row_count,affected_rows,result_bytes,created_at,updated_at)
		values ($1,$2,'write',false,decode(repeat('11',32),'hex'),decode(repeat('22',32),'hex'),
		 decode(repeat('33',32),'hex'),'pending',2,0,0,0,clock_timestamp(),clock_timestamp())`, execution, actor); err != nil {
		t.Fatalf("insert valid execution: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.sql_console_write_receipts
		(execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
		values ($1,$2,decode(repeat('11',32),'hex'),decode(repeat('22',32),'hex'),2,2,200,clock_timestamp())`, execution, actor); err != nil {
		t.Fatalf("insert valid receipt: %v", err)
	}

	requireSQLConsoleConstraint(t, ctx, pool, `insert into pgbase_meta.sql_console_executions
		(execution_id,administrator_id,mode,dry_run,request_hash,query_fingerprint,status,statement_count,row_count,affected_rows,result_bytes,created_at,updated_at)
		values ('b3638df6-7912-482c-b09f-660c7a71d4c0',$1,'write',false,decode(repeat('22',32),'hex'),
		decode(repeat('33',32),'hex'),'pending',1,0,0,0,clock_timestamp(),clock_timestamp())`, actor)
	requireSQLConsoleConstraint(t, ctx, pool, `update pgbase_meta.sql_console_executions
		set status='succeeded', committed=false, finished_at=clock_timestamp(), updated_at=clock_timestamp()
		where execution_id=$1`, execution)
	requireSQLConsoleConstraint(t, ctx, pool, `update pgbase_meta.sql_console_executions
		set result_bytes=4194305 where execution_id=$1`, execution)
}

func requireSQLConsoleConstraint(t *testing.T, ctx context.Context, pool *database.Pool, statement string, arguments ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, arguments...); err == nil {
		t.Fatal("statement unexpectedly passed a SQL console constraint")
	} else if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23514" {
		t.Fatalf("constraint error = %v", err)
	}
}
