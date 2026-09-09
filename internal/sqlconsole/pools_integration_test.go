package sqlconsole

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestOpenPoolsAcceptsOnlyLeastPrivilegeRolesAndSafeTargets(t *testing.T) {
	fixture := newPoolFixture(t)

	pools, err := OpenPools(context.Background(), fixture.options)
	if err != nil {
		t.Fatalf("OpenPools: %v", err)
	}
	pools.Close()

	if _, err := fixture.owner.Exec(context.Background(), "grant select (secret) on app.accounts to "+pgx.Identifier{fixture.readRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(context.Background(), fixture.options); err == nil {
		pools.Close()
		t.Fatal("read role with password-column access was accepted")
	}
}

func TestOpenPoolsRejectsExtensionSideEffects(t *testing.T) {
	fixture := newPoolFixture(t)
	ctx := context.Background()
	if _, err := fixture.owner.Exec(ctx, `create function ext.touch_managed() returns trigger
		language plpgsql as $$begin update app.accounts set visible='changed'; return new; end$$;
		create trigger items_touch before update on ext.items for each row execute function ext.touch_managed()`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("write target with a user trigger was accepted")
	}
}

type poolFixture struct {
	owner    *database.Pool
	options  PoolOptions
	readRole string
}

func newPoolFixture(t *testing.T) poolFixture {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	owner, err := database.Open(ctx, databaseURL, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	for _, migration := range system.Migrations() {
		if _, err := owner.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	compilerRole := "n8_compiler_" + suffix
	readRole := "n8_read_" + suffix
	writerRole := "n8_writer_" + suffix
	password := "n8-test-password-" + suffix
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	identifiers := []string{
		pgx.Identifier{compilerRole}.Sanitize(),
		pgx.Identifier{readRole}.Sanitize(),
		pgx.Identifier{writerRole}.Sanitize(),
	}
	if _, err := owner.Exec(ctx, fmt.Sprintf(`
		create schema ext;
		create table app.accounts (
			id uuid primary key, created_at timestamptz not null, updated_at timestamptz not null,
			visible text, secret text
		);
		create table ext.items (id uuid primary key, amount numeric not null, label text not null);
		revoke all on database %s from public;
		revoke all on schema app,ext from public;
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		create role %s login password %s noinherit nosuperuser nocreatedb nocreaterole noreplication nobypassrls;
		grant connect,temp on database %s to %s;
		grant connect on database %s to %s,%s;
		grant usage on schema app,ext to %s,%s;
		grant usage on schema ext to %s;
		grant select (id,created_at,updated_at,visible) on app.accounts to %s,%s,%s;
		grant select (id,amount,label) on ext.items to %s,%s,%s;
		grant insert (id,amount,label),update (id,amount,label),delete on ext.items to %s;
		alter role %s in database %s set default_transaction_read_only=on`,
		pgx.Identifier{databaseName}.Sanitize(),
		identifiers[0], quoteLiteral(password), identifiers[1], quoteLiteral(password), identifiers[2], quoteLiteral(password),
		pgx.Identifier{databaseName}.Sanitize(), identifiers[0],
		pgx.Identifier{databaseName}.Sanitize(), identifiers[1], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2], identifiers[0], identifiers[1], identifiers[2], identifiers[2],
		identifiers[1], pgx.Identifier{databaseName}.Sanitize(),
	)); err != nil {
		t.Fatalf("provision N8 roles: %v", err)
	}
	t.Cleanup(func() {
		for _, identifier := range identifiers {
			_, _ = owner.Exec(context.Background(), "drop owned by "+identifier)
			_, _ = owner.Exec(context.Background(), "drop role if exists "+identifier)
		}
	})

	fieldVisibleID := uuid.New()
	fieldSecretID := uuid.New()
	catalog := schema.Catalog{Version: 1, Collections: map[string]schema.Collection{
		"accounts": {
			ID: uuid.New(), Name: "accounts", TableName: "accounts",
			Fields: []schema.Field{
				{ID: fieldVisibleID, Name: "visible", ColumnName: "visible", Type: schema.TypeText},
				{ID: fieldSecretID, Name: "secret", ColumnName: "secret", Type: schema.TypePassword, Hidden: true},
			},
		},
	}}
	return poolFixture{
		owner: owner, readRole: readRole,
		options: PoolOptions{
			CompilerURL:  roleURL(parsed, compilerRole, password),
			ReadURL:      roleURL(parsed, readRole, password),
			WriterURL:    roleURL(parsed, writerRole, password),
			Catalog:      catalog,
			WriteTargets: []WriteTarget{{Schema: "ext", Table: "items", Columns: []string{"id", "amount", "label"}}},
		},
	}
}

func roleURL(base *url.URL, role, password string) string {
	copyOfURL := *base
	copyOfURL.User = url.UserPassword(role, password)
	query := copyOfURL.Query()
	query.Set("pool_max_conns", "4")
	copyOfURL.RawQuery = query.Encode()
	return copyOfURL.String()
}

func quoteLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
