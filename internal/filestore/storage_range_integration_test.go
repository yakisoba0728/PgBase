//go:build integration

package filestore

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestStorageControllerRangeRetainsLeaseUntilBodyClose(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, []byte("0123456789abcdef0123456789abcdef"), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/11111111-1111-4111-8111-111111111111"
	if err := controller.Put(ctx, key, bytes.NewReader([]byte("range bytes"))); err != nil {
		t.Fatal(err)
	}
	result, err := controller.OpenRange(ctx, key, ByteRange{Start: 0, End: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Body.Close() })
	body, err := io.ReadAll(result.Body)
	if err != nil || string(body) != "range" || result.Start != 0 || result.End != 4 || result.Size != 11 {
		t.Fatalf("range=%q start/end/size=%d/%d/%d err=%v", body, result.Start, result.End, result.Size, err)
	}
	updated := make(chan error, 1)
	go func() {
		_, err := controller.Update(ctx, StorageUpdate{Driver: "local", Revision: 1})
		updated <- err
	}()
	select {
	case err := <-updated:
		t.Fatalf("storage settings changed while range body remained open: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("range close did not release storage transition")
	}
	invalid, err := controller.OpenRange(ctx, key, ByteRange{Start: 30, End: 40})
	if !errors.Is(err, ErrRangeNotSatisfiable) || invalid.Size != 11 || len(controller.operations) != 0 {
		t.Fatalf("failed range lost size or retained its lease: size=%d operations=%d err=%v", invalid.Size, len(controller.operations), err)
	}
}
