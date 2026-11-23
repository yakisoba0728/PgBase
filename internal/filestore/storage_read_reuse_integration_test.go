//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func openReadReuseFixture(t *testing.T) (*database.Pool, *StorageController, string) {
	t.Helper()
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{9}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/" + uuid.NewString()
	if err := controller.Put(ctx, key, bytes.NewReader(bytes.Repeat([]byte("r"), 4096))); err != nil {
		t.Fatal(err)
	}
	return pool, controller, key
}

// transitionLeaseHolders lists backends holding a shared destination lease.
func transitionLeaseHolders(t *testing.T, pool *database.Pool) []int32 {
	t.Helper()
	rows, err := pool.Query(t.Context(), `select pid from pg_locks where locktype='advisory' and mode='ShareLock' and granted
		and database=(select oid from pg_database where datname=current_database())
		and classid=($1::bigint >> 32)::oid and objid=($1::bigint & 4294967295)::oid and objsubid=1 order by pid`, storageTransitionLock)
	if err != nil {
		t.Fatal(err)
	}
	pids, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		t.Fatal(err)
	}
	return pids
}

// Downloads reuse one coordination session instead of connecting for every
// read, and still hold and then drop the destination lease exactly as before.
func TestObjectReadsReuseCoordinationSessionAndReleaseLease(t *testing.T) {
	pool, controller, key := openReadReuseFixture(t)
	ctx := t.Context()
	var first int32
	for iteration := range 5 {
		reader, err := controller.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		holders := transitionLeaseHolders(t, pool)
		if len(holders) != 1 {
			t.Fatalf("iteration %d: open read lease holders=%v, want one", iteration, holders)
		}
		if iteration == 0 {
			first = holders[0]
		} else if holders[0] != first {
			t.Fatalf("iteration %d: read used backend %d, want reused backend %d", iteration, holders[0], first)
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if holders := transitionLeaseHolders(t, pool); len(holders) != 0 {
			t.Fatalf("iteration %d: closed read kept its lease on %v", iteration, holders)
		}
		if active := len(controller.operations); active != 0 {
			t.Fatalf("iteration %d: closed read kept %d admission slots", iteration, active)
		}
	}
	// A destination transition is not blocked by the idle reusable session.
	transitionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.InTx(transitionCtx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return lockStorageTransition(transitionCtx, tx)
	}); err != nil {
		t.Fatalf("transition after reads: %v", err)
	}
	ranged, err := controller.OpenRange(ctx, key, ByteRange{Start: 0, End: 9})
	if err != nil {
		t.Fatal(err)
	}
	if holders := transitionLeaseHolders(t, pool); len(holders) != 1 || holders[0] != first {
		t.Fatalf("range read lease holders=%v, want reused backend %d", holders, first)
	}
	if err := ranged.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if holders := transitionLeaseHolders(t, pool); len(holders) != 0 {
		t.Fatalf("closed range read kept its lease on %v", holders)
	}
}

// Cancellation closes a read lease's socket from another goroutine (#192). A
// canceled read's session must never be handed to the next read, and its lease
// must still end promptly whichever of cancellation and Close runs first.
func TestCanceledObjectReadNeverReturnsItsSessionForReuse(t *testing.T) {
	pool, controller, key := openReadReuseFixture(t)
	ctx := t.Context()
	for iteration := range 100 {
		readCtx, cancel := context.WithCancel(ctx)
		reader, err := controller.Open(readCtx, key)
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(time.Duration(rand.IntN(500)) * time.Microsecond)
			cancel()
		}()
		_, _ = io.Copy(io.Discard, reader)
		if iteration%2 == 0 {
			<-done
		}
		_ = reader.Close()
		<-done
		deadline := time.Now().Add(5 * time.Second)
		for len(transitionLeaseHolders(t, pool)) != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("iteration %d: canceled read kept its lease", iteration)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if active := len(controller.operations); active != 0 {
			t.Fatalf("iteration %d: canceled read kept %d admission slots", iteration, active)
		}
		// The next read must get a live session, not one whose socket the
		// cancellation closed.
		next, err := controller.Open(ctx, key)
		if err != nil {
			t.Fatalf("iteration %d: read after cancellation: %v", iteration, err)
		}
		if _, err := io.Copy(io.Discard, next); err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if err := next.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
