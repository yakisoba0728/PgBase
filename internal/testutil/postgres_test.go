package testutil

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWithDatabaseNameReplacesDatabase(t *testing.T) {
	connectionURL := withDatabaseName(
		"postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
		"pgbase_test_0123456789abcdef0123456789abcdef",
	)
	config, err := pgx.ParseConfig(connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.Database != "pgbase_test_0123456789abcdef0123456789abcdef" {
		t.Fatalf("database = %q", config.Database)
	}
}
