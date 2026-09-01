// Package testutil contains test-only PostgreSQL helpers.
package testutil

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NewDatabase creates an isolated PostgreSQL database and drops it when t ends.
func NewDatabase(t *testing.T) string {
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
