//go:build integration

package runtimesettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestLegacyRateLabelsReloadButMustBeRepairedBeforeUpdate(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("r"), 32)
	controller, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	legacy := controller.Get()
	legacy.RateLimits.Enabled = true
	legacy.RateLimits.Rules = []APIRateRule{
		{Label: "posts:list", Duration: 60, MaxRequests: 5},
		{Label: "GET /health/live", Duration: 60, MaxRequests: 10},
	}
	// Simulate a document accepted and encrypted by a previous release. The
	// current Update path must not accept this ineffective label as a new write.
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controller.encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, `update pgbase_meta.runtime_settings
		set document_ciphertext=$1,document=null,revision=revision+1 where singleton returning revision`, ciphertext).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := controller.Reload(ctx); err != nil {
		t.Fatalf("reload legacy encrypted settings must remain repairable: %v", err)
	}
	restarted, err := New(ctx, pool, key)
	if err != nil {
		t.Fatalf("startup with legacy encrypted settings: %v", err)
	}
	loaded := restarted.Get()
	if loaded.Revision != revision || !reflect.DeepEqual(loaded.RateLimits, legacy.RateLimits) {
		t.Fatalf("legacy configuration changed during reload: %#v", loaded.RateLimits)
	}
	invalid := loaded
	invalid.Application.Branding = "unrelated setting edit"
	if _, err := restarted.Update(ctx, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new update must reject unrepaired label: %v", err)
	}
	if got := restarted.Get(); got.Revision != revision || got.Application.Branding != loaded.Application.Branding {
		t.Fatal("rejected update changed published settings")
	}
	repaired := restarted.Get()
	repaired.RateLimits.Rules = repaired.RateLimits.Rules[1:]
	saved, err := restarted.Update(ctx, repaired)
	if err != nil {
		t.Fatalf("save repaired route rules: %v", err)
	}
	if saved.Revision != revision+1 {
		t.Fatalf("repair revision=%d, want %d", saved.Revision, revision+1)
	}
	if err := controller.Reload(ctx); err != nil {
		t.Fatalf("another controller reloads repaired rules: %v", err)
	}
	if got := controller.Get(); !reflect.DeepEqual(got.RateLimits.Rules, repaired.RateLimits.Rules) {
		t.Fatalf("repaired rules did not persist: %#v", got.RateLimits.Rules)
	}
}
