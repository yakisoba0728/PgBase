//go:build integration

package runtimesettings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestControllersRejectStaleCASAndNeverRegressPublishedRevision(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	a := first.Get()
	a.Batch.Enabled = false
	if _, err := first.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := second.Get()
	b.Batch.MaxRequests = 10
	if _, err := second.Update(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v, want conflict", err)
	}
	if err := second.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if got := second.Get(); got.Revision != 2 || got.Batch.Enabled {
		t.Fatalf("reloaded snapshot=%#v", got)
	}
	// A stale read cannot overwrite a newer local snapshot.
	second.mu.Lock()
	second.value.Revision = 3
	second.mu.Unlock()
	if err := second.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if got := second.Get().Revision; got != 3 {
		t.Fatalf("reload regressed published revision to %d", got)
	}
}
