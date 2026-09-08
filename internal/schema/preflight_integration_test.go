//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestCatalogPreflightIsANoOpBeforeCatalogTablesExist(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cache := &Cache{}
	if err := PreflightCatalog(context.Background(), pool, cache); err != nil {
		t.Fatalf("fresh database preflight: %v", err)
	}
	if cache.current.Load() != nil {
		t.Fatal("fresh database preflight published a catalog")
	}
}

func TestCatalogPreflightAcceptsVersion21BeforeFieldContractMigration(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, migration := range system.Migrations() {
		if migration.Version > 21 {
			break
		}
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("migration %d: %v", migration.Version, err)
		}
	}
	if err := PreflightCatalog(ctx, pool, &Cache{}); err != nil {
		t.Fatalf("v21 preflight: %v", err)
	}
}

func TestCatalogPreflightRejectsOversizedLegacyCatalogWithoutPublishingIt(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "legacy_catalog_limit",
		Fields: []FieldInput{{Name: "body", Type: TypeText}},
	}, administratorID)

	legacyDefault, err := json.Marshal(strings.Repeat("x", MaximumTextBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.fields
		set has_default = true, default_json = $2::jsonb
		where collection_id = $1`, created.ID, legacyDefault); err != nil {
		t.Fatalf("plant legacy catalog: %v", err)
	}

	cache := &Cache{}
	err = preflightCatalogWithLimit(context.Background(), pool, cache, 512, nil)
	if !errors.Is(err, ErrCatalogLimitExceeded) {
		t.Fatalf("preflight error = %v, want ErrCatalogLimitExceeded", err)
	}
	if got := cache.Load().Version; got != 0 {
		t.Fatalf("published catalog version = %d, want zero after rejection", got)
	}
}

func TestCatalogPreflightPublishesSnapshotThatListenerReuses(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "preflight_reuse",
		Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, administratorID)

	cache := &Cache{}
	if err := PreflightCatalog(context.Background(), pool, cache); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	preflightSnapshot := cache.current.Load()
	if preflightSnapshot == nil || preflightSnapshot.catalog.Version != 1 {
		t.Fatalf("preflight snapshot = %#v, want version 1", preflightSnapshot)
	}

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	listener := NewListener(pool, cache, nil, service)
	if _, err := listener.reload(context.Background(), connection.Conn()); err != nil {
		t.Fatalf("listener reload: %v", err)
	}
	if got := cache.current.Load(); got != preflightSnapshot {
		t.Fatal("listener recompiled the catalog snapshot published by preflight")
	}
}

func TestCatalogPreflightDiscardsPreparedSnapshotWhenReadTransactionCannotCommit(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "preflight_rollback",
		Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, administratorID)

	cache := &Cache{}
	ctx, cancel := context.WithCancel(context.Background())
	err := preflightCatalogWithLimit(ctx, pool, cache, maximumCatalogWireBytes, cancel)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("preflight error = %v, want context cancellation at commit", err)
	}
	if cache.current.Load() != nil {
		t.Fatal("preflight published a snapshot from a transaction that did not commit")
	}
	cache.compileMu.Lock()
	defer cache.compileMu.Unlock()
	if len(cache.prepared) != 0 {
		t.Fatalf("prepared snapshots after rollback = %d, want zero", len(cache.prepared))
	}
}
