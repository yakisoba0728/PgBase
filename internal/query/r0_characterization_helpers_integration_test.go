//go:build integration

package query

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

const r0AdministratorID = "019551d0-e9c0-7000-8000-000000000001"

func newR0QueryServiceTest(t *testing.T) (string, *database.Pool, *schema.Service, *Service) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		VALUES ($1, 'r0-query@example.test', 'test', true, current_timestamp, current_timestamp)`,
		uuid.MustParse(r0AdministratorID)); err != nil {
		t.Fatal(err)
	}
	testutil.RetireRecordMembershipV4(t, ctx, pool)
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	return databaseURL, pool, schemas, NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"))
}

func applyR0QueryCollection(t *testing.T, schemas *schema.Service, input schema.CreateCollectionInput) schema.Collection {
	t.Helper()
	ctx := context.Background()
	plan, err := schemas.PlanCreate(ctx, input)
	if err != nil {
		t.Fatalf("plan collection %s: %v", input.Name, err)
	}
	collection, err := schemas.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, uuid.MustParse(r0AdministratorID))
	if err != nil {
		t.Fatalf("apply collection %s: %v", input.Name, err)
	}
	return collection
}
