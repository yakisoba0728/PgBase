//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAuthorizedAuthDeleteRechecksRuleAfterTargetLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := service.Register(ctx, "delete-race@example.test", "correct horse battery staple", "delete-race")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.users set verified=true where id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set delete_rule='verified = true' where id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `update pgbase_meta.users set email_visibility=email_visibility where id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `update pgbase_meta.auth_collections set delete_rule='false' where id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- service.DeleteAuthorizedAuthRecord(ctx, DefaultCollectionID, session.User.ID, session.User.ID)
	}()
	// Wait until deletion reaches its target lock, after any stale rule read.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%pgbase_meta.users%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("delete completed before waiting for target lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("delete after record stopped satisfying rule: %v", err)
	}
	if _, err := service.GetAuthRecord(ctx, DefaultCollectionID, session.User.ID); err != nil {
		t.Fatalf("record denied by delete rule was removed: %v", err)
	}
}
