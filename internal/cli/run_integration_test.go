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

func TestMigrateUpThroughEmitsExactMachineReadableReceipt(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	if code := Run(context.Background(), []string{"migrate", "up"}, lookup, io.Discard, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("initial migrate up exit code = %d", code)
	}
	migrations := system.Migrations()
	target := migrations[len(migrations)-1]
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "up", "--through", fmt.Sprint(target.Version)}, lookup, &stdout, &stderr, DefaultCommands()); code != 0 {
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
	wantWire := fmt.Sprintf(
		`{"receiptVersion":1,"target":{"version":%d,"checksum":%q},"before":{"version":%d,"checksum":%q,"dirty":false},"after":{"version":%d,"checksum":%q,"dirty":false}}`+"\n",
		target.Version, wantChecksum, target.Version, wantChecksum, target.Version, wantChecksum,
	)
	if stdout.String() != wantWire {
		t.Fatalf("receipt wire = %q, want %q", stdout.String(), wantWire)
	}
	if receipt.ReceiptVersion != 1 || receipt.Target.Version != target.Version || receipt.Target.Checksum != wantChecksum {
		t.Fatalf("receipt target = %#v", receipt)
	}
	if receipt.Before.Version != target.Version || receipt.Before.Checksum != wantChecksum || receipt.Before.Dirty {
		t.Fatalf("receipt before = %#v", receipt.Before)
	}
	if receipt.After.Version != target.Version || receipt.After.Checksum != wantChecksum || receipt.After.Dirty {
		t.Fatalf("receipt after = %#v", receipt.After)
	}
}

func TestMigrateUpThroughRejectsMoreThanOnePendingMigrationWithoutDDL(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	target := migrations[len(migrations)-1].Version
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "up", "--through", fmt.Sprint(target)}, lookup, &stdout, &stderr, DefaultCommands())
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
	if code := Run(context.Background(), []string{"migrate", "up"}, lookup, io.Discard, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("initial migrate up exit code = %d", code)
	}
	target := system.Migrations()[len(system.Migrations())-1].Version
	preflightCalls := 0
	var firstCache *schema.Cache
	var stdout, stderr bytes.Buffer
	err := runMigrateWithCatalogPreflight(
		context.Background(),
		[]string{"up", "--through", fmt.Sprint(target)},
		lookup,
		&stdout,
		&stderr,
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
