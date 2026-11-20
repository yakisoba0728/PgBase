//go:build integration

package runtimesettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestSettingsRejectAmbiguousNewBackupScheduleAndPreserveLegacy(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := New(ctx, pool, bytes.Repeat([]byte("t"), 32))
	if err != nil {
		t.Fatal(err)
	}
	legacy := controller.Get()
	legacy.Backups.Schedule, legacy.Backups.Timezone = "0 0 * * *", "Local"
	if _, err := controller.Update(ctx, legacy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("save new Local schedule: %v", err)
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptDocument(controller.key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.runtime_settings SET document_ciphertext=$1,revision=revision+1 WHERE singleton`, ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := controller.Reload(ctx); err != nil {
		t.Fatalf("reload legacy schedule: %v", err)
	}
	next := controller.Get()
	next.Backups.Keep = 7
	if next, err = controller.Update(ctx, next); err != nil || next.Backups.Timezone != "Local" {
		t.Fatalf("apply unrelated setting while retaining legacy timezone: %+v, %v", next.Backups, err)
	}
	next.Backups.Schedule, next.Backups.Timezone = "TZ=UTC 0 0 * * *", "UTC"
	if _, err := controller.Update(ctx, next); !errors.Is(err, ErrInvalid) {
		t.Fatalf("save new inline timezone: %v", err)
	}
	next.Backups.Schedule = "0 0 * * *"
	if _, err := controller.Update(ctx, next); err != nil {
		t.Fatalf("repair legacy backup schedule: %v", err)
	}
}

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
	notifications := 0
	second.Subscribe(func(Settings) { notifications++ })
	if err := second.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := second.Get(); got.Revision != 2 || got.Batch.Enabled {
		t.Fatalf("refreshed snapshot=%#v", got)
	}
	if err := second.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatalf("revision refresh published %d times, want one publication", notifications)
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

func TestRotationRejectsStaleOldKeyUpdate(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	oldKey := bytes.Repeat([]byte("a"), 32)
	newKey := bytes.Repeat([]byte("b"), 32)
	rotating, err := New(ctx, pool, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := New(ctx, pool, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotating.RotateKey(ctx, newKey); err != nil {
		t.Fatal(err)
	}
	candidate := stale.Get()
	candidate.Application.Branding = "must not overwrite rotated ciphertext"
	if _, err := stale.Update(ctx, candidate); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale old-key update error = %v, want conflict", err)
	}
	rotated, err := New(ctx, pool, newKey)
	if err != nil {
		t.Fatalf("new-key restart failed after stale update: %v", err)
	}
	if got := rotated.Get(); got.Application.Branding != "" {
		t.Fatalf("stale update overwrote rotated ciphertext: %#v", got)
	}
}
