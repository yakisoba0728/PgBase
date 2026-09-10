//go:build integration

package runtimesettings

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
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
	key := bytes.Repeat([]byte("a"), 32)
	first, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(ctx, pool, key)
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

func TestSettingsAreEncryptedAndKeyRotationPreservesRevisionAndPublication(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	firstKey := bytes.Repeat([]byte("a"), 32)
	secondKey := bytes.Repeat([]byte("b"), 32)
	controller, err := New(ctx, pool, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	updated := controller.Get()
	updated.Application.Branding = "settings plaintext sentinel"
	updated, err = controller.Update(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	var document *string
	var ciphertext []byte
	var revision int64
	if err := pool.QueryRow(ctx, `select document::text,document_ciphertext,revision from pgbase_meta.runtime_settings where singleton`).Scan(&document, &ciphertext, &revision); err != nil {
		t.Fatal(err)
	}
	if document != nil || len(ciphertext) == 0 || strings.Contains(string(ciphertext), "settings plaintext sentinel") {
		t.Fatalf("runtime settings persisted in plaintext: document=%v ciphertext=%q", document, ciphertext)
	}
	published := controller.Get()
	if err := controller.RotateKey(ctx, secondKey); err != nil {
		t.Fatal(err)
	}
	if got := controller.Get(); !reflect.DeepEqual(got, published) {
		t.Fatalf("rotation republished settings: got=%#v want=%#v", got, published)
	}
	if err := pool.QueryRow(ctx, `select revision from pgbase_meta.runtime_settings where singleton`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != updated.Revision {
		t.Fatalf("rotation changed revision to %d, want %d", revision, updated.Revision)
	}
	reloaded, err := New(ctx, pool, secondKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(); !reflect.DeepEqual(got, updated) {
		t.Fatalf("rotated reload=%#v want=%#v", got, updated)
	}
}

func TestWrongKeyReloadPreservesPublishedSnapshot(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("a"), 32)
	controller, err := New(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	published := controller.Get()
	wrong := &Controller{pool: pool, key: deriveKey(bytes.Repeat([]byte("b"), 32)), value: published}
	if err := wrong.Reload(ctx); err == nil {
		t.Fatal("wrong-key reload unexpectedly succeeded")
	}
	if got := wrong.Get(); !reflect.DeepEqual(got, published) {
		t.Fatalf("wrong-key reload changed published snapshot: got=%#v want=%#v", got, published)
	}
}
