package testutil

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRequiredAdminURLFailsClosedWhenEnvironmentIsMissing(t *testing.T) {
	_, err := requiredAdminURL(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "PGBASE_TEST_ADMIN_URL") {
		t.Fatalf("requiredAdminURL() error = %v, want named missing-environment error", err)
	}

	const want = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	got, err := requiredAdminURL(func(name string) string {
		if name != "PGBASE_TEST_ADMIN_URL" {
			t.Fatalf("environment lookup = %q", name)
		}
		return want
	})
	if err != nil || got != want {
		t.Fatalf("requiredAdminURL() = %q, %v; want %q, nil", got, err, want)
	}
}

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
