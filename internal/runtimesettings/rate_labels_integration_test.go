//go:build integration

package runtimesettings

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestInvalidRateLabelCannotReplacePublishedOrPersistedSettings(t *testing.T) {
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
	candidate := controller.Get()
	candidate.RateLimits.Enabled = true
	candidate.RateLimits.Rules = []APIRateRule{{Label: "GET /api/v1/collections/{collection}/records", Duration: 60, MaxRequests: 10}}
	published, err := controller.Update(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate = controller.Get()
	candidate.RateLimits.Rules[0].Label = "posts:list"
	if _, err := controller.Update(ctx, candidate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid label update = %v, want ErrInvalid", err)
	}
	if got := controller.Get(); !reflect.DeepEqual(got, published) {
		t.Fatalf("invalid update changed published settings: got %#v want %#v", got, published)
	}
	reloaded, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(); !reflect.DeepEqual(got, published) {
		t.Fatalf("invalid update changed persisted settings: got %#v want %#v", got, published)
	}
}
