//go:build integration

package jsonvalue

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestValidPostgresNumberMatchesPostgreSQLAtScaleBoundaries(t *testing.T) {
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(ctx) })

	for _, raw := range []string{
		"1e131071", "1e131072", "0e131072",
		"1e-16383", "1e-16384", "1.0e-16382", "1.0e-16383",
		"0e-16383", "0e-16384", "0.0e-16382", "0.0e-16383",
		"1000e-16380", "1000e-16385",
	} {
		var value string
		postgresErr := connection.QueryRow(ctx, "select ($1::text)::jsonb::text", raw).Scan(&value)
		if got, want := ValidPostgresNumber(raw), postgresErr == nil; got != want {
			t.Fatalf("ValidPostgresNumber(%q) = %t, PostgreSQL acceptance = %t (error %v)", raw, got, want, postgresErr)
		}
	}
}
