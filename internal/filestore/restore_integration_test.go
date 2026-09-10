//go:build integration

package filestore

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestResetRestoredStorageDetachesSourceNamespaceAndWork(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_settings(singleton,driver,s3_bucket,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style) values(true,'s3','source','region','https://source.invalid','source-key',$1,true)`, bytes.Repeat([]byte{3}, 48)); err != nil {
		t.Fatal(err)
	}
	migrationID, key := uuid.New(), "objects/"+uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_migrations(id,status,base_revision,source_config_ciphertext,destination_config_ciphertext,request_hash) values($1,'committed',1,$2,$2,$3)`, migrationID, []byte("source snapshot"), bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_migration_objects(migration_id,object_key,object_kind,status) values($1,$2,'original','verified')`, migrationID, key); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_source_cleanup(migration_id,object_key,source_config_ciphertext,not_before) values($1,$2,$3,CURRENT_TIMESTAMP)`, migrationID, key, []byte("source snapshot")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,CURRENT_TIMESTAMP)`, key); err != nil {
		t.Fatal(err)
	}
	if err := ResetRestoredStorage(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var driver, bucket, endpoint, access string
	var secret []byte
	var pathStyle bool
	if err := pool.QueryRow(ctx, `select driver,s3_bucket,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style from pgbase_meta.file_storage_settings where singleton`).Scan(&driver, &bucket, &endpoint, &access, &secret, &pathStyle); err != nil {
		t.Fatal(err)
	}
	if driver != "local" || bucket != "" || endpoint != "" || access != "" || len(secret) != 0 || pathStyle {
		t.Fatalf("restored storage retained source configuration: driver=%q bucket=%q endpoint=%q access=%q secret=%d pathStyle=%t", driver, bucket, endpoint, access, len(secret), pathStyle)
	}
	var migrations, cleanup, deletions int
	if err := pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.file_storage_migrations),(select count(*) from pgbase_meta.file_storage_source_cleanup),(select count(*) from pgbase_meta.file_object_deletions)`).Scan(&migrations, &cleanup, &deletions); err != nil {
		t.Fatal(err)
	}
	if migrations != 0 || cleanup != 0 || deletions != 0 {
		t.Fatalf("restored worker state migrations=%d cleanup=%d deletions=%d", migrations, cleanup, deletions)
	}
}
