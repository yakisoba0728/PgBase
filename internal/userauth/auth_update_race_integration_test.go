//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAuthorizedAuthUpdateRechecksRuleAfterTargetLock(t *testing.T) {
	t.Run("record", func(t *testing.T) { runAuthUpdateRuleRace(t, false) })
	t.Run("profile", func(t *testing.T) { runAuthUpdateRuleRace(t, true) })
}
func runAuthUpdateRuleRace(t *testing.T, profile bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := service.Register(ctx, "update-race@example.test", "correct horse battery staple", "update-race")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.users set verified=true where id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule='verified = true' where id=$1`, DefaultCollectionID); err != nil {
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
	if _, err := blocker.Exec(ctx, `update pgbase_meta.auth_collections set update_rule='false' where id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		if profile {
			_, err := service.SetProfile(ctx, session.User.ID, map[string]any{})
			result <- err
			return
		}
		visibility := true
		_, err := service.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, session.User.ID, session.User.ID, UpdateAuthRecordInput{EmailVisibility: &visibility})
		result <- err
	}()
	// Wait until update reaches its target lock, after any stale rule read.
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
			t.Fatalf("update completed before waiting for target lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantErr := ErrInvalidCredentials
	if profile {
		wantErr = ErrValidation
	}
	if err := <-result; !errors.Is(err, wantErr) {
		t.Fatalf("update after record stopped satisfying rule: %v", err)
	}
	if value, err := service.GetAuthRecord(ctx, DefaultCollectionID, session.User.ID); err != nil || value.EmailVisibility {
		t.Fatalf("record denied by update rule was removed: %v", err)
	}
}
