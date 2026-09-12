//go:build integration

package pgbase

import (
	"context"
	"errors"
	"testing"
)

func TestTrustedDatabaseOptInAndNestedTransactionsStayBound(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.EnableTrustedDatabaseAccess = true })
	database, err := fixture.app.TrustedDatabase()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.RunInTransaction(ctx, func(txCtx context.Context) error {
		if _, err := database.Exec(txCtx, `create temporary table extension_tx(value text)`); err != nil {
			return err
		}
		if _, err := database.Exec(txCtx, `insert into extension_tx(value) values ('outer')`); err != nil {
			return err
		}
		failure := errors.New("rollback savepoint")
		if err := database.RunInTransaction(txCtx, func(nestedCtx context.Context) error {
			if _, err := NewTrustedQuery(`insert into extension_tx(value) values ($1)`, "nested").Exec(nestedCtx, database); err != nil {
				return err
			}
			return failure
		}); !errors.Is(err, failure) {
			return err
		}
		var count int
		if err := database.QueryRow(txCtx, `select count(*) from extension_tx`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("count inside outer transaction=%d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDatabaseContextCannotCrossAppAndNestedPanicRollsBack(t *testing.T) {
	firstFixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.EnableTrustedDatabaseAccess = true })
	secondFixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.EnableTrustedDatabaseAccess = true })
	first, _ := firstFixture.app.TrustedDatabase()
	second, _ := secondFixture.app.TrustedDatabase()
	var secondName string
	if err := second.QueryRow(context.Background(), `select current_database()`).Scan(&secondName); err != nil {
		t.Fatal(err)
	}
	if err := first.RunInTransaction(context.Background(), func(txCtx context.Context) error {
		var crossName string
		if err := second.QueryRow(txCtx, `select current_database()`).Scan(&crossName); err != nil {
			return err
		}
		if crossName != secondName {
			t.Fatalf("cross app query used database %q, want %q", crossName, secondName)
		}
		if _, err := first.Exec(txCtx, `create temporary table panic_tx(value text)`); err != nil {
			return err
		}
		if _, err := first.Exec(txCtx, `insert into panic_tx values ('outer')`); err != nil {
			return err
		}
		func() {
			defer func() { _ = recover() }()
			_ = first.RunInTransaction(txCtx, func(nested context.Context) error {
				_, _ = first.Exec(nested, `insert into panic_tx values ('nested')`)
				panic("fixture")
			})
		}()
		var count int
		if err := first.QueryRow(txCtx, `select count(*) from panic_tx`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("count after recovered nested panic=%d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDatabaseRequiresExplicitOptIn(t *testing.T) {
	app := &App{pool: nil, options: Options{}}
	if _, err := app.TrustedDatabase(); err == nil {
		t.Fatal("expected unavailable error")
	}
}
