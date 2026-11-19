// Package testutil contains test-only PostgreSQL helpers.
package testutil

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func NewDatabase(t testing.TB) string {
	t.Helper()

	adminURL, err := requiredAdminURL(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}

	databaseName := "pgbase_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test admin database: %v", err)
	}

	if _, err := adminConn.Exec(ctx, "create database "+databaseIdentifier); err != nil {
		if closeErr := adminConn.Close(ctx); closeErr != nil {
			t.Fatalf("create PostgreSQL test database: %v; close admin connection: %v", err, closeErr)
		}
		t.Fatalf("create PostgreSQL test database: %v", err)
	}

	t.Cleanup(func() {
		defer func() {
			if err := adminConn.Close(ctx); err != nil {
				t.Errorf("close PostgreSQL test admin connection: %v", err)
			}
		}()
		if _, err := adminConn.Exec(ctx, `select pg_terminate_backend(pid)
			from pg_stat_activity
			where datname = $1 and pid <> pg_backend_pid()`, databaseName); err != nil {
			t.Errorf("terminate connections to PostgreSQL test database: %v", err)
		}
		if _, err := adminConn.Exec(ctx, "drop database if exists "+databaseIdentifier); err != nil {
			t.Errorf("drop PostgreSQL test database: %v", err)
		}
	})

	return withDatabaseName(adminURL, databaseName)
}

// WaitForDatabaseSessionsToClose waits for PostgreSQL to process the
// termination messages sent by already-closed fixture connections.
func WaitForDatabaseSessionsToClose(t testing.TB, databaseURL string) {
	t.Helper()
	adminURL, err := requiredAdminURL(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var idle bool
		err := connection.QueryRow(ctx, `select not exists(
			select from pg_stat_activity where datname=$1 and backend_type='client backend'
		)`, strings.TrimPrefix(target.Path, "/")).Scan(&idle)
		if err != nil {
			t.Fatalf("check fixture PostgreSQL sessions: %v", err)
		}
		if idle {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("PostgreSQL fixture sessions are still open")
		case <-ticker.C:
		}
	}
}

func requiredAdminURL(lookup func(string) string) (string, error) {
	const name = "PGBASE_TEST_ADMIN_URL"
	value := lookup(name)
	if value == "" {
		return "", errors.New(name + " is required for PostgreSQL integration tests")
	}
	return value, nil
}

func withDatabaseName(adminURL, databaseName string) string {
	parsed, err := url.Parse(adminURL)
	if err != nil {
		panic(err)
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	return parsed.String()
}
