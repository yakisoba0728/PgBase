//go:build integration

package pgbase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestFailedConstructionReleasesOwnedDatabaseResources(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "catalog", true: "after-services"}[late], func(t *testing.T) {
			ctx := context.Background()
			url := testutil.NewDatabase(t)
			options := Options{DatabaseURL: url, TokenSecret: make([]byte, 32), AutoMigrate: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if late {
				invalid := filepath.Join(t.TempDir(), "invalid.pem")
				if err := os.WriteFile(invalid, []byte("not a certificate"), 0600); err != nil {
					t.Fatal(err)
				}
				options.TLS = &TLSOptions{Address: "127.0.0.1:0", CertificateFile: invalid, KeyFile: invalid}
			}
			var acquired *database.Pool
			app, err := newWithCatalogPreflight(ctx, options, func(ctx context.Context, pool *database.Pool, cache *schema.Cache) error {
				acquired = pool
				if !late {
					return errors.New("injected catalog failure")
				}
				return schema.PreflightCatalog(ctx, pool, cache)
			})
			if err == nil || app != nil || acquired == nil {
				t.Fatalf("construction did not reach expected failure: app=%v pool=%v err=%v", app != nil, acquired != nil, err)
			}
			if err := acquired.Ping(ctx); err == nil {
				t.Fatal("failed construction retained its pool")
			}
			gate, err := database.TryBackupGate(ctx, url, true, false)
			if err != nil {
				t.Fatalf("failed construction retained a writer gate or pooled connection: %v", err)
			}
			if err := gate.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
