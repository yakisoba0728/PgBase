//go:build integration

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestMigrateUpAndStatus(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	if len(migrations) == 0 {
		t.Fatal("embedded system migrations are empty")
	}
	latest := migrations[len(migrations)-1].Version
	wantCurrent := fmt.Sprintf("current=%d", latest)
	wantLatest := fmt.Sprintf("latest=%d", latest)

	var upOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "up"}, lookup, &upOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate up exit code = %d, stdout = %q", code, upOutput.String())
	}
	if !strings.Contains(upOutput.String(), wantCurrent) || !strings.Contains(upOutput.String(), wantLatest) {
		t.Fatalf("migrate up stdout = %q", upOutput.String())
	}

	var statusOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "status"}, lookup, &statusOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate status exit code = %d, stdout = %q", code, statusOutput.String())
	}
	if !strings.Contains(statusOutput.String(), wantCurrent) || !strings.Contains(statusOutput.String(), "dirty=false") {
		t.Fatalf("migrate status stdout = %q", statusOutput.String())
	}
}

func TestMigrateUpBlocksRolloutAfterUpgradingAnOlderDatabaseWhenCatalogPreflightFails(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	var stdout, stderr bytes.Buffer
	preflightCalls := 0
	var rolloutCache *schema.Cache
	err := runMigrateWithCatalogPreflight(
		context.Background(),
		[]string{"up"},
		lookup,
		&stdout,
		&stderr,
		func(ctx context.Context, pool *database.Pool, cache *schema.Cache) error {
			preflightCalls++
			var catalogTablesAvailable bool
			if err := pool.QueryRow(ctx, `select to_regclass('pgbase_meta.field_relations') is not null`).Scan(&catalogTablesAvailable); err != nil {
				return err
			}
			if want := preflightCalls == 2; catalogTablesAvailable != want {
				return fmt.Errorf("catalog tables available on preflight %d = %t, want %t", preflightCalls, catalogTablesAvailable, want)
			}
			if preflightCalls == 1 {
				rolloutCache = cache
				return nil
			}
			if cache != rolloutCache {
				t.Fatal("migrate up did not reuse its catalog cache across preflight calls")
			}
			return schema.ErrCatalogLimitExceeded
		},
	)
	if preflightCalls != 2 {
		t.Fatalf("catalog preflight calls = %d, want pre- and post-migration checks", preflightCalls)
	}
	if !errors.Is(err, schema.ErrCatalogLimitExceeded) {
		t.Fatalf("migrate up error = %v, want ErrCatalogLimitExceeded", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("migrate up printed a success receipt after preflight failed: %q", stdout.String())
	}

	pool, openErr := database.Open(context.Background(), databaseURL, time.Second)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer pool.Close()
	status, statusErr := migrate.New(pool, nil).Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Dirty || status.Current != status.Latest {
		t.Fatalf("migration status = %#v, want upgraded current database before catalog preflight", status)
	}
}
