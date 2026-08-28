package database

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestOpenRejectsPoolTooSmallForListenerAndRequests(t *testing.T) {
	parsed, err := url.Parse(testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("pool_max_conns", "1")
	parsed.RawQuery = query.Encode()
	_, err = Open(context.Background(), parsed.String(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("Open error = %v, want minimum pool size rejection", err)
	}
}

func TestOpenAppliesPgBaseSessionSettings(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var statementTimeout, applicationName, timezone string
	if err := pool.QueryRow(context.Background(), `
		select current_setting('statement_timeout'), current_setting('application_name'), current_setting('timezone')`).Scan(
		&statementTimeout, &applicationName, &timezone,
	); err != nil {
		t.Fatal(err)
	}
	if statementTimeout != "1500ms" {
		t.Fatalf("statement_timeout = %q, want %q", statementTimeout, "1500ms")
	}
	if applicationName != "pgbase" {
		t.Fatalf("application_name = %q, want %q", applicationName, "pgbase")
	}
	if timezone != "UTC" {
		t.Fatalf("timezone = %q, want %q", timezone, "UTC")
	}
}

func TestCloseContextHonorsDeadlineWithBorrowedConnection(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = pool.CloseContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CloseContext error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("CloseContext waited too long for borrowed connection: %s", elapsed)
	}

	connection.Release()
	pool.Close()
}
