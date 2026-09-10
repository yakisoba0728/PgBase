//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestTextDefaultLiteralRoundTripsWithStandardConformingStringsDisabled(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "set standard_conforming_strings = off"); err != nil {
		t.Fatalf("disable standard_conforming_strings: %v", err)
	}

	want := `C:\temp\O'Brien`
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	_, rendered, err := normalizeDefault(TypeText, raw)
	if err != nil {
		t.Fatalf("render text default: %v", err)
	}
	var got string
	if err := connection.QueryRow(ctx, "select "+rendered).Scan(&got); err != nil {
		t.Fatalf("evaluate rendered default %q: %v", rendered, err)
	}
	if got != want {
		t.Fatalf("rendered default round trip = %q, want %q (SQL %q)", got, want, rendered)
	}
}
