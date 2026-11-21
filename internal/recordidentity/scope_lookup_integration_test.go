//go:build integration

package recordidentity

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLookupsResolveOnlyTheRequestedCollectionScope(t *testing.T) {
	ctx, pool, first := identityFixture(t, "identity_scope_first")
	_, _, second := identityFixtureOnPool(t, ctx, pool, "identity_scope_second")
	service := New(nil)
	firstInternal, secondInternal := uuid.New(), uuid.New()
	if _, err := service.Reserve(ctx, pool, first, firstInternal, stringPointer("SharedKey01")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, pool, second, secondInternal, stringPointer("SharedKey01")); err != nil {
		t.Fatalf("same external ID in another base collection: %v", err)
	}
	if _, err := service.Reserve(ctx, pool, second, uuid.New(), stringPointer("OnlySecond01")); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Resolve(ctx, pool, first, "SharedKey01"); err != nil || got != firstInternal {
		t.Fatalf("first Resolve = %s, %v", got, err)
	}
	if got, err := service.Resolve(ctx, pool, second, "SharedKey01"); err != nil || got != secondInternal {
		t.Fatalf("second Resolve = %s, %v", got, err)
	}
	if _, err := service.Resolve(ctx, pool, first, "OnlySecond01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-collection Resolve error = %v, want ErrNotFound", err)
	}
	ids, err := service.LookupMany(ctx, pool, first, []string{"OnlySecond01", "SharedKey01", "SharedKey01"})
	if err != nil || len(ids) != 3 || ids[0] != uuid.Nil || ids[1] != firstInternal || ids[2] != firstInternal {
		t.Fatalf("first LookupMany = %v, %v", ids, err)
	}
	ids, err = service.LookupMany(ctx, pool, second, []string{"SharedKey01", "OnlySecond01"})
	if err != nil || len(ids) != 2 || ids[0] != secondInternal || ids[1] == uuid.Nil {
		t.Fatalf("second LookupMany = %v, %v", ids, err)
	}
	if _, err := service.ResolveMany(ctx, pool, first, []string{"SharedKey01", "OnlySecond01"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-collection ResolveMany error = %v, want ErrNotFound", err)
	}

	// Auth identities share one scope, so another auth collection's ID must
	// still be absent from this collection rather than resolve across it.
	firstAuth, secondAuth := uuid.New(), uuid.New()
	for index, id := range []uuid.UUID{firstAuth, secondAuth} {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,record_id,created_at,updated_at) values($1,$2,$3::jsonb,now(),now())`, id, []string{"identity_scope_auth_first", "identity_scope_auth_second"}[index], `{"kind":"text","minLength":1,"maxLength":32,"pattern":"^[A-Za-z0-9]+$"}`); err != nil {
			t.Fatal(err)
		}
	}
	authInternal := uuid.New()
	if _, err := service.ReserveAuth(ctx, pool, firstAuth, authInternal, first.RecordID, stringPointer("SharedKey01")); err != nil {
		t.Fatalf("auth scope rejected an external ID used by base collections: %v", err)
	}
	if got, err := service.ResolveAuth(ctx, pool, firstAuth, first.RecordID, "SharedKey01"); err != nil || got != authInternal {
		t.Fatalf("auth Resolve = %s, %v", got, err)
	}
	if _, err := service.ResolveAuth(ctx, pool, secondAuth, first.RecordID, "SharedKey01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-auth-collection Resolve error = %v, want ErrNotFound", err)
	}
	if got, err := service.Resolve(ctx, pool, first, "SharedKey01"); err != nil || got != firstInternal {
		t.Fatalf("base Resolve after auth reservation = %s, %v", got, err)
	}
}
