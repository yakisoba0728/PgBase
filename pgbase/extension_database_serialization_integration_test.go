//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// The host must build a representable result before committing its SQL batch.
// Otherwise callers receive an error even though a preceding write committed,
// and retrying the failed batch can repeat its effects.
func TestExtensionDatabaseSerializationFailureRollsBack(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "owned"
		if nested {
			name = "savepoint"
		}
		t.Run(name, func(t *testing.T) {
			pool, err := database.Open(t.Context(), testutil.NewDatabase(t), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if _, err := pool.Exec(t.Context(), `CREATE TABLE public.extension_response_probe (id integer PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			trusted := &TrustedDatabase{pool: pool}
			host := &extensionHost{bridge: &extensionBridge{database: trusted}}
			for _, number := range []string{"NaN", "Infinity", "-Infinity"} {
				t.Run(number, func(t *testing.T) {
					if _, err := pool.Exec(t.Context(), `TRUNCATE public.extension_response_probe`); err != nil {
						t.Fatal(err)
					}
					request, err := json.Marshal(extensionDatabaseRequest{Statements: []extensionDatabaseStatement{
						{SQL: `INSERT INTO public.extension_response_probe VALUES (1)`},
						{SQL: `SELECT $1::float8 AS value`, Args: []json.RawMessage{json.RawMessage(`"` + number + `"`)}, Query: true},
					}})
					if err != nil {
						t.Fatal(err)
					}
					invoke := func(ctx context.Context) error {
						response, callErr := host.callDatabase(ctx, request)
						if callErr == nil || len(response) != 0 {
							t.Fatalf("unrepresentable result was accepted: response=%s err=%v", response, callErr)
						}
						return nil
					}
					if nested {
						err = trusted.RunInTransaction(t.Context(), func(ctx context.Context) error {
							if _, err := trusted.Exec(ctx, `INSERT INTO public.extension_response_probe VALUES (2)`); err != nil {
								return err
							}
							return invoke(ctx)
						})
					} else {
						err = invoke(t.Context())
					}
					if err != nil {
						t.Fatal(err)
					}
					var failedBatch, outerWork int
					if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE id=1), count(*) FILTER (WHERE id=2) FROM public.extension_response_probe`).Scan(&failedBatch, &outerWork); err != nil {
						t.Fatal(err)
					}
					wantOuter := 0
					if nested {
						wantOuter = 1
					}
					if failedBatch != 0 || outerWork != wantOuter {
						t.Errorf("failed batch committed or unrelated outer work was lost: failed=%d outer=%d wantOuter=%d", failedBatch, outerWork, wantOuter)
					}
				})
			}
			response, err := host.callDatabase(t.Context(), json.RawMessage(`{"statements":[{"sql":"INSERT INTO public.extension_response_probe VALUES (3)"},{"sql":"SELECT 1.5::float8 AS value","query":true}]}`))
			if err != nil || !json.Valid(response) {
				t.Fatalf("representable batch failed: response=%s err=%v", response, err)
			}
			var committed bool
			if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM public.extension_response_probe WHERE id=3)`).Scan(&committed); err != nil || !committed {
				t.Fatalf("representable batch did not commit: committed=%v err=%v", committed, err)
			}
		})
	}
}
