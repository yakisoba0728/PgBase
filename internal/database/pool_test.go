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

func TestPoolStatsReturnsCompleteSnapshot(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stats := pool.Stats()
	connection.Release()

	if stats.MaxConns < 2 {
		t.Fatalf("MaxConns = %d, want at least 2", stats.MaxConns)
	}
	if stats.TotalConns < 1 {
		t.Fatalf("TotalConns = %d, want at least 1", stats.TotalConns)
	}
	if stats.AcquiredConns < 1 {
		t.Fatalf("AcquiredConns = %d, want at least 1", stats.AcquiredConns)
	}
	if stats.AcquireCount < 2 { // Open pings once and this test acquires once.
		t.Fatalf("AcquireCount = %d, want at least 2", stats.AcquireCount)
	}
	if stats.AcquireDuration <= 0 {
		t.Fatalf("AcquireDuration = %s, want positive", stats.AcquireDuration)
	}
}

func TestNilPoolStatsIsZeroSnapshot(t *testing.T) {
	var pool *Pool
	if got := pool.Stats(); got != (PoolStats{}) {
		t.Fatalf("Stats() = %+v, want zero snapshot", got)
	}
}

func TestOpenAcceptsValidatedStatementTimeoutUnits(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	tests := []struct {
		name        string
		timeout     time.Duration
		wantSetting string
	}{
		{name: "minutes", timeout: 90 * time.Second, wantSetting: "90s"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := Open(context.Background(), databaseURL, test.timeout)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			defer pool.Close()

			var got string
			if err := pool.QueryRow(context.Background(), "select current_setting('statement_timeout')").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != test.wantSetting {
				t.Fatalf("statement_timeout = %q, want %q", got, test.wantSetting)
			}
		})
	}
}

func TestPostgresDurationRoundsPositiveSubMillisecondValuesUpWithoutGoUnits(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 0, want: "0"},
		{duration: 100 * time.Microsecond, want: "1"},
		{duration: time.Millisecond, want: "1"},
		{duration: 90 * time.Second, want: "90000"},
	}
	for _, test := range tests {
		if got := postgresDuration(test.duration); got != test.want {
			t.Fatalf("postgresDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
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
