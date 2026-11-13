//go:build integration

package userauth

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestKeyedSessionRefreshReusesTransactionConnection(t *testing.T) {
	ctx := context.Background()
	databaseURL, err := url.Parse(testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("pool_max_conns", "4")
	databaseURL.RawQuery = query.Encode()
	pool, err := database.Open(ctx, databaseURL.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if _, err := service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "session", TTLSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	session, err := service.Register(ctx, "refresh-pool@example.test", "correct horse battery staple", "refresh-pool")
	if err != nil {
		t.Fatal(err)
	}
	// Leave exactly one request connection available for the entire refresh.
	for range 3 {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	refreshed, err := service.RefreshSessionForCollection(refreshCtx, DefaultCollectionID, session.RawToken)
	if err != nil {
		t.Fatalf("refresh with one available connection: %v", err)
	}
	if refreshed.RawToken == session.RawToken || refreshed.User.ID != session.User.ID {
		t.Fatalf("session was not rotated for the original user: %+v", refreshed)
	}
	if _, err := service.Authenticate(refreshCtx, refreshed.RawToken); err != nil {
		t.Fatalf("authenticate refreshed session: %v", err)
	}
}
