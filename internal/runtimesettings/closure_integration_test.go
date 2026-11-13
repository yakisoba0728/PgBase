//go:build integration

package runtimesettings

import (
	"bytes"
	"errors"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"net/netip"
	"testing"
	"time"
)

func TestStartupProxySeedPreservesExplicitEmptySavedValue(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("s"), 32)
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	first, err := NewWithTrustedProxies(ctx, pool, key, proxies)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.TrustedProxies(); len(got) != 1 || got[0] != proxies[0] {
		t.Fatalf("startup proxies=%v", got)
	}
	candidate := first.Get()
	candidate.Proxy.TrustedProxyCIDRs = nil
	if _, err := first.Update(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	second, err := NewWithTrustedProxies(ctx, pool, key, proxies)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.TrustedProxies(); len(got) != 0 {
		t.Fatalf("overwrote explicit empty setting: %v", got)
	}
}
func TestInvalidBackupScheduleDoesNotPublishOrPersist(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("b"), 32)
	controller, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	initial := controller.Get()
	notifications := 0
	controller.Subscribe(func(Settings) { notifications++ })
	for _, test := range []struct{ schedule, zone string }{{"garbage", "UTC"}, {"@every 1s", "Mars/Base"}, {"0 0 * * *", "Mars/Base"}} {
		candidate := initial
		candidate.Backups.Enabled = false
		candidate.Backups.Schedule = test.schedule
		candidate.Backups.Timezone = test.zone
		if _, err := controller.Update(ctx, candidate); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", test, err)
		}
		if got := controller.Get(); got.Revision != initial.Revision || got.Backups != initial.Backups {
			t.Fatalf("published invalid snapshot: %+v", got)
		}
	}
	if notifications != 0 {
		t.Fatalf("invalid settings notified runtime jobs: %d", notifications)
	}
	restarted, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Get(); got.Revision != initial.Revision || got.Backups != initial.Backups {
		t.Fatalf("persisted invalid snapshot: %+v", got)
	}
}
