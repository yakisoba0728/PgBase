//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestStorageControllerForeignLeaseKeepsObjectNamespaceAndPublicationOwner(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	newController := func() *StorageController {
		t.Helper()
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{9}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return controller
	}
	first, second := newController(), newController()
	key := "objects/11111111-1111-4111-8111-111111111111"
	for _, fixture := range []struct {
		controller *StorageController
		value      string
	}{{first, "first"}, {second, "second"}} {
		if err := fixture.controller.Put(ctx, key, strings.NewReader(fixture.value)); err != nil {
			t.Fatal(err)
		}
	}
	firstContext, release, err := first.BeginStorageOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return first.GuardStoragePublication(firstContext, tx)
	}); err != nil {
		t.Fatalf("own lease publication rejected: %v", err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return second.GuardStoragePublication(firstContext, tx)
	}); !errors.Is(err, ErrStorageConflict) {
		t.Errorf("foreign lease authorized publication: %v", err)
	}
	read := func(ctx context.Context, controller *StorageController) string {
		t.Helper()
		reader, err := controller.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(reader)
		if err := errors.Join(readErr, reader.Close()); err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := read(firstContext, second); got != "second" {
		t.Errorf("second controller read foreign namespace: %q", got)
	}
	if err := second.Put(firstContext, key, strings.NewReader("updated second")); err != nil {
		t.Fatal(err)
	}
	if got := read(ctx, first); got != "first" {
		t.Errorf("second controller changed first object: %q", got)
	}
	if got := read(ctx, second); got != "updated second" {
		t.Errorf("second controller did not update its object: %q", got)
	}
	if err := second.Delete(firstContext, key); err != nil {
		t.Fatal(err)
	}
	if got := read(ctx, first); got != "first" {
		t.Errorf("second controller deleted first object: %q", got)
	}
	if reader, err := second.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Errorf("second controller retained deleted object: %v", err)
	}
	if len(first.operations) != 1 || len(second.operations) != 0 {
		t.Errorf("unexpected lease ownership: first=%d second=%d", len(first.operations), len(second.operations))
	}
}
