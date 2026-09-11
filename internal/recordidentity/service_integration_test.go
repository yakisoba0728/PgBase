//go:build integration

package recordidentity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestTextIdentityExactRoundTripAndNoUUIDFallback(t *testing.T) {
	ctx, pool, collection := identityFixture(t, "identity_exact")
	service := New(nil)
	upperID, lowerID := uuid.New(), uuid.New()

	if got, err := service.Reserve(ctx, pool, collection, upperID, stringPointer("CaseSensitive001")); err != nil || got != "CaseSensitive001" {
		t.Fatalf("reserve exact ID = %q, %v", got, err)
	}
	if got, err := service.Reserve(ctx, pool, collection, lowerID, stringPointer("casesensitive001")); err != nil || got != "casesensitive001" {
		t.Fatalf("reserve case-distinct ID = %q, %v", got, err)
	}
	if got, err := service.Resolve(ctx, pool, collection, "CaseSensitive001"); err != nil || got != upperID {
		t.Fatalf("resolve exact ID = %s, %v", got, err)
	}
	if got, err := service.Public(ctx, pool, collection, lowerID); err != nil || got != "casesensitive001" {
		t.Fatalf("public exact ID = %q, %v", got, err)
	}
	unmapped := strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := service.Resolve(ctx, pool, collection, unmapped); !errors.Is(err, ErrNotFound) {
		t.Fatalf("text policy UUID fallback error = %v, want ErrNotFound", err)
	}
}

func TestCallerIdentityCollisionIsDeterministicUnderConcurrency(t *testing.T) {
	ctx, pool, collection := identityFixture(t, "identity_collision")
	service := New(nil)
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(context.Background()) }()
	if _, err := service.Reserve(ctx, first, collection, uuid.New(), stringPointer("ConcurrentKey01")); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		second, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			result <- beginErr
			return
		}
		defer func() { _ = second.Rollback(context.Background()) }()
		_, reserveErr := service.Reserve(ctx, second, collection, uuid.New(), stringPointer("ConcurrentKey01"))
		result <- reserveErr
	}()
	select {
	case err := <-result:
		t.Fatalf("colliding reservation returned before first transaction completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("collision error = %v, want ErrConflict", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("colliding reservation did not finish")
	}
}

func TestRolledBackReservationCanBeReused(t *testing.T) {
	ctx, pool, collection := identityFixture(t, "identity_rollback")
	service := New(nil)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, tx, collection, uuid.New(), stringPointer("RollbackKey0001")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	want := uuid.New()
	if _, err := service.Reserve(ctx, pool, collection, want, stringPointer("RollbackKey0001")); err != nil {
		t.Fatalf("reuse rolled back ID: %v", err)
	}
	if got, err := service.Resolve(ctx, pool, collection, "RollbackKey0001"); err != nil || got != want {
		t.Fatalf("resolve reused ID = %s, %v", got, err)
	}
}

func TestProjectionMapsRootAndRelationIdentities(t *testing.T) {
	ctx, pool, source := identityFixture(t, "identity_projection_source")
	_, _, target := identityFixtureOnPool(t, ctx, pool, "identity_projection_target")
	fieldID := uuid.New()
	source.Fields = []schema.Field{{ID: fieldID, Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: target.ID}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{source.Name: source, target.Name: target}}
	service := New(nil)
	sourceInternal, targetInternal := uuid.New(), uuid.New()
	if _, err := service.Reserve(ctx, pool, source, sourceInternal, stringPointer("SourceIdentity1")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, pool, target, targetInternal, stringPointer("TargetIdentity1")); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"id": sourceInternal.String(), "target": targetInternal.String()}
	if err := service.Project(ctx, pool, catalog, source, record); err != nil {
		t.Fatal(err)
	}
	if record["id"] != "SourceIdentity1" || record["target"] != "TargetIdentity1" {
		t.Fatalf("projected record = %#v", record)
	}
}

func TestCollectionOwnershipKeyIncludesRealmKind(t *testing.T) {
	ctx, pool, base := identityFixture(t, "identity_cross_kind")
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,record_id,created_at,updated_at) values($1,'identity_cross_kind_auth',$2::jsonb,now(),now())`, base.ID, `{"kind":"text","minLength":1,"maxLength":32,"pattern":"^[A-Za-z0-9]+$"}`); err != nil {
		t.Fatalf("same UUID auth/base collection ownership: %v", err)
	}
	var owners int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_identity_collections where id=$1`, base.ID).Scan(&owners); err != nil || owners != 2 {
		t.Fatalf("identity owners=%d err=%v", owners, err)
	}
	service, baseInternal, authInternal := New(nil), uuid.New(), uuid.New()
	if _, err := service.Reserve(ctx, pool, base, baseInternal, stringPointer("SharedPublic01")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReserveAuth(ctx, pool, base.ID, authInternal, base.RecordID, stringPointer("SharedPublic01")); err != nil {
		t.Fatalf("separate base/auth scopes rejected same external ID: %v", err)
	}
	if got, err := service.Resolve(ctx, pool, base, "SharedPublic01"); err != nil || got != baseInternal {
		t.Fatalf("base resolve=%s err=%v", got, err)
	}
	if got, err := service.ResolveAuth(ctx, pool, base.ID, base.RecordID, "SharedPublic01"); err != nil || got != authInternal {
		t.Fatalf("auth resolve=%s err=%v", got, err)
	}
}

func identityFixture(t *testing.T, name string) (context.Context, *database.Pool, schema.Collection) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range system.Migrations() {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
	return identityFixtureOnPool(t, ctx, pool, name)
}

func identityFixtureOnPool(t *testing.T, ctx context.Context, pool *database.Pool, name string) (context.Context, *database.Pool, schema.Collection) {
	t.Helper()
	collection := schema.Collection{ID: uuid.New(), Name: name, TableName: name, RecordID: schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,record_id,created_at,updated_at) values($1,$2,$2,'active',$3::jsonb,transaction_timestamp(),transaction_timestamp())`, collection.ID, collection.Name, `{"kind":"text","minLength":1,"maxLength":32,"pattern":"^[A-Za-z0-9]+$"}`); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, collection
}

func stringPointer(value string) *string { return &value }
