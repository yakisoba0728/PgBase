//go:build integration

package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestBackupRejectsPersistedS3Storage(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close(ctx) }()
	if err := validateBackupStorage(ctx, pool); err != nil {
		t.Fatalf("legacy database: %v", err)
	}
	if _, err := pool.Exec(ctx, `create schema pgbase_meta; create table pgbase_meta.file_storage_settings(singleton boolean primary key,driver text not null)`); err != nil {
		t.Fatal(err)
	}
	if err := validateBackupStorage(ctx, pool); err != nil {
		t.Fatalf("uninitialized storage: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_settings values(true,'local')`); err != nil {
		t.Fatal(err)
	}
	if err := validateBackupStorage(ctx, pool); err != nil {
		t.Fatalf("local storage: %v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.file_storage_settings set driver='s3'`); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "restore"} {
		err := runBackup(ctx, []string{operation, "--directory", t.TempDir()}, migrationEnv(databaseURL), testIO(nil, io.Discard, io.Discard))
		if err == nil || !strings.Contains(err.Error(), "backup supports only local file storage") {
			t.Fatalf("%s with persisted S3: %v", operation, err)
		}
	}
}
