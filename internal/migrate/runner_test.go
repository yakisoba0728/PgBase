package migrate

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestUpIsIdempotentAndCreatesSchemas(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	var count int
	err = pool.QueryRow(context.Background(),
		"select count(*) from pgbase_meta.system_migrations where version = 1").Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	var appSchema bool
	if err := pool.QueryRow(context.Background(), "select exists(select 1 from pg_namespace where nspname = 'app')").Scan(&appSchema); err != nil {
		t.Fatal(err)
	}
	if !appSchema {
		t.Fatal("app schema was not created")
	}
}

func TestUpRejectsChangedChecksum(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.system_migrations set checksum = decode(repeat('00', 32), 'hex') where version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err == nil {
		t.Fatal("Up succeeded after a checksum change")
	}
}

func TestUpSerializesConcurrentRunners(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runners := []*Runner{
		New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
		New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	start := make(chan struct{})
	errs := make(chan error, len(runners))
	var wg sync.WaitGroup
	for _, runner := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- runner.Up(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.system_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("history row count = %d, want 1", count)
	}
}

func TestStatusReportsAppliedAndLatestVersions(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	before, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Current != 0 || before.Latest != 1 || before.Dirty {
		t.Fatalf("status before Up = %#v", before)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Current != 1 || after.Latest != 1 || after.Dirty {
		t.Fatalf("status after Up = %#v", after)
	}
}
