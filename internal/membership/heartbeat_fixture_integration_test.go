//go:build integration

package membership

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

type bootstrapFixture struct {
	ctx          context.Context
	databaseURL  string
	pool         *database.Pool
	service      *Service
	collectionID uuid.UUID
	tableName    string
}

const bootstrapCapabilityUpdate = `update pgbase_meta.record_membership_capability set
	state = 'ready', generation = 1, minimum_writer_version = $2,
	cursor_issue_version = 5, cutover_at = $1,
	cutover_collection_count = $3, cutover_record_count = 0,
	ready_at = $1, ready_collection_count = $3, v5_enabled_at = $1,
	v4_decode_until = $1, v4_retired_at = $1, updated_at = $1
	where singleton`

func newBootstrapFixture(t *testing.T) bootstrapFixture {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID := uuid.MustParse("0199b700-0000-7000-8000-000000000001")
	createdAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'bootstrap_records', 'bootstrap_records', 'active', $2, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, 0, 0, $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`,
		collectionID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table app.bootstrap_records (
		id uuid primary key,
		created_at timestamptz not null,
		updated_at timestamptz not null
	)`); err != nil {
		t.Fatal(err)
	}
	return bootstrapFixture{
		ctx: ctx, databaseURL: databaseURL, pool: pool, service: New(pool),
		collectionID: collectionID, tableName: "bootstrap_records",
	}
}
