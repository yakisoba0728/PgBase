//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
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
	if code := Execute(context.Background(), []string{"migrate", "up"}, lookup, testIO(nil, &upOutput, io.Discard)); code != 0 {
		t.Fatalf("migrate up exit code = %d, stdout = %q", code, upOutput.String())
	}
	if !strings.Contains(upOutput.String(), wantCurrent) || !strings.Contains(upOutput.String(), wantLatest) {
		t.Fatalf("migrate up stdout = %q", upOutput.String())
	}

	var statusOutput bytes.Buffer
	if code := Execute(context.Background(), []string{"migrate", "status"}, lookup, testIO(nil, &statusOutput, io.Discard)); code != 0 {
		t.Fatalf("migrate status exit code = %d, stdout = %q", code, statusOutput.String())
	}
	if !strings.Contains(statusOutput.String(), wantCurrent) || !strings.Contains(statusOutput.String(), "dirty=false") {
		t.Fatalf("migrate status stdout = %q", statusOutput.String())
	}
}

func TestMigrateUpThroughEmitsExactMachineReadableReceipt(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	if len(migrations) < 2 {
		t.Fatal("exact-target CLI test requires two migrations")
	}
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:len(migrations)-1])
	target := migrations[len(migrations)-1]
	before := migrations[len(migrations)-2]
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"migrate", "up", "--through", fmt.Sprint(target.Version)}, lookup, testIO(nil, &stdout, &stderr)); code != 0 {
		t.Fatalf("migrate up --through exit code = %d, stderr = %q", code, stderr.String())
	}

	var receipt struct {
		ReceiptVersion int `json:"receiptVersion"`
		Target         struct {
			Version  int64  `json:"version"`
			Checksum string `json:"checksum"`
		} `json:"target"`
		Before struct {
			Version  int64  `json:"version"`
			Checksum string `json:"checksum"`
			Dirty    bool   `json:"dirty"`
		} `json:"before"`
		After struct {
			Version  int64  `json:"version"`
			Checksum string `json:"checksum"`
			Dirty    bool   `json:"dirty"`
		} `json:"after"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode exact-target receipt %q: %v", stdout.String(), err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("receipt contains trailing data: %q", stdout.String())
	}
	wantChecksum := hex.EncodeToString(target.SHA256[:])
	wantBeforeChecksum := hex.EncodeToString(before.SHA256[:])
	wantWire := fmt.Sprintf(
		`{"receiptVersion":1,"target":{"version":%d,"checksum":%q},"before":{"version":%d,"checksum":%q,"dirty":false},"after":{"version":%d,"checksum":%q,"dirty":false}}`+"\n",
		target.Version, wantChecksum, before.Version, wantBeforeChecksum, target.Version, wantChecksum,
	)
	if stdout.String() != wantWire {
		t.Fatalf("receipt wire = %q, want %q", stdout.String(), wantWire)
	}
	if receipt.ReceiptVersion != 1 || receipt.Target.Version != target.Version || receipt.Target.Checksum != wantChecksum {
		t.Fatalf("receipt target = %#v", receipt)
	}
	if receipt.Before.Version != before.Version || receipt.Before.Checksum != wantBeforeChecksum || receipt.Before.Dirty {
		t.Fatalf("receipt before = %#v", receipt.Before)
	}
	if receipt.After.Version != target.Version || receipt.After.Checksum != wantChecksum || receipt.After.Dirty {
		t.Fatalf("receipt after = %#v", receipt.After)
	}
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	requireSchemaOperationsPhysicalDDL(t, pool)
}

func TestMigrateUpThroughRejectsMoreThanOnePendingMigrationWithoutDDL(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	target := migrations[len(migrations)-1].Version
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"migrate", "up", "--through", fmt.Sprint(target)}, lookup, testIO(nil, &stdout, &stderr))
	if code != 1 {
		t.Fatalf("exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("rejected exact-target migration emitted a receipt: %q", stdout.String())
	}

	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	status, err := migrate.New(pool, nil).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 0 || status.Dirty {
		t.Fatalf("rejected exact-target migration changed history: %#v", status)
	}
	var appSchemaExists bool
	if err := pool.QueryRow(context.Background(), `select to_regnamespace('app') is not null`).Scan(&appSchemaExists); err != nil {
		t.Fatal(err)
	}
	if appSchemaExists {
		t.Fatal("rejected exact-target migration executed migration DDL")
	}
}

func TestMigrateUpThroughSuppressesReceiptWhenPostMigrationCatalogPreflightFails(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	if len(migrations) < 2 {
		t.Fatal("exact-target CLI test requires two migrations")
	}
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:len(migrations)-1])
	target := migrations[len(migrations)-1].Version
	preflightCalls := 0
	var firstCache *schema.Cache
	var stdout, stderr bytes.Buffer
	err := runMigrateWithCatalogPreflight(
		context.Background(),
		[]string{"up", "--through", fmt.Sprint(target)},
		lookup,
		testIO(nil, &stdout, &stderr),
		func(_ context.Context, _ *database.Pool, cache *schema.Cache) error {
			preflightCalls++
			if preflightCalls == 1 {
				firstCache = cache
				return nil
			}
			if cache != firstCache {
				t.Fatal("exact-target migration did not reuse its catalog cache")
			}
			return schema.ErrCatalogLimitExceeded
		},
	)
	if preflightCalls != 2 {
		t.Fatalf("preflight calls = %d, want two", preflightCalls)
	}
	if !errors.Is(err, schema.ErrCatalogLimitExceeded) {
		t.Fatalf("migrate up --through error = %v, want ErrCatalogLimitExceeded", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed rollout preflight emitted exact-target receipt: %q", stdout.String())
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
	if status.Current != target || status.Dirty {
		t.Fatalf("post-preflight failure did not preserve committed exact target: %#v", status)
	}
	requireSchemaOperationsPhysicalDDL(t, pool)
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
		testIO(nil, &stdout, &stderr),
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

func requireSchemaOperationsPhysicalDDL(t *testing.T, pool *database.Pool) {
	t.Helper()
	var tableExists, stateConstraintExists bool
	if err := pool.QueryRow(context.Background(), `
		select
			to_regclass('pgbase_meta.schema_operations') is not null,
			exists (
				select 1
				from pg_constraint constraint_definition
				join pg_class relation on relation.oid = constraint_definition.conrelid
				join pg_namespace namespace on namespace.oid = relation.relnamespace
				where namespace.nspname = 'pgbase_meta'
					and relation.relname = 'schema_operations'
					and constraint_definition.conname = 'schema_operations_state_check'
			)
	`).Scan(&tableExists, &stateConstraintExists); err != nil {
		t.Fatal(err)
	}
	if !tableExists {
		t.Fatal("exact-target migration did not create pgbase_meta.schema_operations")
	}
	if !stateConstraintExists {
		t.Fatal("exact-target migration did not create schema_operations_state_check")
	}
}
