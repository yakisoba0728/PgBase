//go:build integration

package sqlconsole

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
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
	if len(pools.dependencies.functions["abs"]) == 0 || len(pools.dependencies.operators["+"]) == 0 || pools.dependencies.casts["numeric"] == 0 {
		t.Fatalf("native dependency OIDs = %#v", pools.dependencies)
	}
	forged := Plan{Statements: []AnalyzedStatement{{Functions: []FunctionRef{{Schema: "pg_catalog", Name: "pg_read_file"}}}}}
	if err := pools.ValidatePlan(context.Background(), forged); !errors.Is(err, ErrSQLRejected) {
		t.Fatalf("forged dependency error = %v", err)
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

func TestOpenPoolsRejectsWriterWithoutFixedReceiptGrant(t *testing.T) {
	fixture := newPoolFixture(t)
	if _, err := fixture.owner.Exec(context.Background(), `revoke insert (result_bytes)
		on pgbase_meta.sql_console_write_receipts from `+pgx.Identifier{fixture.writerRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(context.Background(), fixture.options); err == nil {
		pools.Close()
		t.Fatal("writer without the fixed receipt grant was accepted")
	}
}

func TestOpenPoolsRejectsUnexpectedUserRelationGrant(t *testing.T) {
	fixture := newPoolFixture(t)
	if _, err := fixture.owner.Exec(context.Background(), `create table ext.unlisted(id uuid);
		grant select on ext.unlisted to `+pgx.Identifier{fixture.readRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(context.Background(), fixture.options); err == nil {
		pools.Close()
		t.Fatal("read role with an unlisted relation grant was accepted")
	}
}

func TestOpenPoolsValidatesManagedViewTransitiveDependencies(t *testing.T) {
	fixture := newPoolFixture(t)
	ctx := context.Background()
	roles := strings.Join([]string{
		pgx.Identifier{fixture.compilerRole}.Sanitize(),
		pgx.Identifier{fixture.readRole}.Sanitize(),
		pgx.Identifier{fixture.writerRole}.Sanitize(),
	}, ",")
	if _, err := fixture.owner.Exec(ctx, `create view app.visible_accounts as
		select id,created_at,updated_at,upper(visible) as visible from app.accounts;
		grant select (id,created_at,updated_at,visible) on app.visible_accounts to `+roles); err != nil {
		t.Fatal(err)
	}
	fixture.options.Catalog.Collections["visible_accounts"] = schema.Collection{
		ID: uuid.New(), Name: "visible_accounts", TableName: "visible_accounts",
		View:   &schema.ViewSpec{Dependencies: []uuid.UUID{fixture.options.Catalog.Collections["accounts"].ID}},
		Fields: []schema.Field{{ID: uuid.New(), Name: "visible", ColumnName: "visible", Type: schema.TypeText}},
	}
	pools, err := OpenPools(ctx, fixture.options)
	if err != nil {
		t.Fatalf("safe managed view: %v", err)
	}
	pools.Close()

	if _, err := fixture.owner.Exec(ctx, `create type ext.review_enum as enum ('x');
		create or replace view app.visible_accounts as
		select id,created_at,updated_at,(visible::ext.review_enum)::text as visible from app.accounts`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("managed view with a transitive user-type IO cast was accepted")
	}
	if _, err := fixture.owner.Exec(ctx, `create or replace view app.visible_accounts as
		select id,created_at,updated_at,(visible::uuid)::text as visible from app.accounts`); err != nil {
		t.Fatal(err)
	}
	pools, err = OpenPools(ctx, fixture.options)
	if err != nil {
		t.Fatalf("safe builtin IO cast: %v", err)
	}
	pools.Close()
	if _, err := fixture.owner.Exec(ctx, `create or replace view app.visible_accounts as
		select id,created_at,updated_at,current_date::text as visible from app.accounts`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("managed view with unhandled SQLValueFunction was accepted")
	}

	if _, err := fixture.owner.Exec(ctx, `create or replace view app.visible_accounts as
		select id,created_at,updated_at,current_setting('server_version') as visible from app.accounts`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("managed view with a stable pg_catalog function outside the exact allowlist was accepted")
	}

	if _, err := fixture.owner.Exec(ctx, `create view ext.unmanaged_accounts as
		select id,created_at,updated_at,upper(visible) as visible from app.accounts;
		create or replace view app.visible_accounts as select * from ext.unmanaged_accounts`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("managed view with a transitive unmanaged relation was accepted")
	}

	if _, err := fixture.owner.Exec(ctx, `create function ext.read_secret() returns text
		language sql volatile as $$select secret from app.accounts limit 1$$;
		revoke execute on function ext.read_secret() from public;
		create or replace view app.visible_accounts as
		select id,created_at,updated_at,ext.read_secret() as visible from app.accounts`); err != nil {
		t.Fatal(err)
	}
	if pools, err := OpenPools(ctx, fixture.options); err == nil {
		pools.Close()
		t.Fatal("managed view with a transitive custom function was accepted")
	}
}

func TestOpenPoolsRejectsPlansAfterCatalogPolicyChanges(t *testing.T) {
	fixture := newPoolFixture(t)
	pools, err := OpenPools(context.Background(), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	runner, err := NewRunner(pools, fixture.owner)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select visible from app.accounts"}}}
	if err := runner.Validate(context.Background(), request); err != nil {
		t.Fatalf("initial policy validation: %v", err)
	}
	fixture.catalogVersion.Store(fixture.options.Catalog.Version + 1)
	if err := runner.Validate(context.Background(), request); !errors.Is(err, ErrCatalogChanged) {
		t.Fatalf("stale policy error = %v", err)
	}
}

func TestOpenPoolsSkipsCatalogFieldsWithoutPhysicalColumns(t *testing.T) {
	fixture := newPoolFixture(t)
	accounts := fixture.options.Catalog.Collections["accounts"]
	accounts.Fields = append(accounts.Fields,
		schema.Field{ID: uuid.New(), Name: "avatar", ColumnName: "avatar", Type: schema.TypeFile},
		schema.Field{ID: uuid.New(), Name: "members", ColumnName: "members", Type: schema.TypeRelation,
			Relation: &schema.RelationSpec{Cardinality: schema.RelationMultiple}},
	)
	fixture.options.Catalog.Collections["accounts"] = accounts
	pools, err := OpenPools(context.Background(), fixture.options)
	if err != nil {
		t.Fatalf("valid virtual fields rejected: %v", err)
	}
	pools.Close()
}

func TestOpenPoolsKeepsConfiguredWriteColumnsExact(t *testing.T) {
	fixture := newPoolFixture(t)
	roles := strings.Join([]string{
		pgx.Identifier{fixture.compilerRole}.Sanitize(), pgx.Identifier{fixture.readRole}.Sanitize(), pgx.Identifier{fixture.writerRole}.Sanitize(),
	}, ",")
	if _, err := fixture.owner.Exec(context.Background(), `revoke select (label) on ext.items from `+roles+`;
		revoke insert (label),update (label) on ext.items from `+pgx.Identifier{fixture.writerRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	fixture.options.WriteTargets[0].Columns = []string{"id", "amount"}
	pools, err := OpenPools(context.Background(), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	runner, _ := NewRunner(pools, fixture.owner)
	request := Request{
		ExecutionID: uuid.New(), Mode: ModeWrite, IdempotencyKey: "exact-write-columns-000000001",
		Statements: []Statement{{SQL: "update ext.items set label='blocked'"}},
	}
	if err := runner.Validate(context.Background(), request); !errors.Is(err, ErrSQLRejected) {
		t.Fatalf("unconfigured write column error = %v", err)
	}
}

type poolFixture struct {
	owner          *database.Pool
	options        PoolOptions
	compilerRole   string
	readRole       string
	writerRole     string
	actorID        uuid.UUID
	catalogVersion *atomic.Int64
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
	actorID := uuid.New()
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
		grant usage on schema ext,pgbase_meta to %s;
		grant select (id,created_at,updated_at,visible) on app.accounts to %s,%s,%s;
		grant select (id,amount,label) on ext.items to %s,%s,%s;
		grant insert (id,amount,label),update (id,amount,label),delete on ext.items to %s;
		grant insert (execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
			on pgbase_meta.sql_console_write_receipts to %s;
		alter role %s in database %s set default_transaction_read_only=on`,
		pgx.Identifier{databaseName}.Sanitize(),
		identifiers[0], quoteLiteral(password), identifiers[1], quoteLiteral(password), identifiers[2], quoteLiteral(password),
		pgx.Identifier{databaseName}.Sanitize(), identifiers[0],
		pgx.Identifier{databaseName}.Sanitize(), identifiers[1], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2],
		identifiers[0], identifiers[1], identifiers[2], identifiers[0], identifiers[1], identifiers[2], identifiers[2], identifiers[2],
		identifiers[1], pgx.Identifier{databaseName}.Sanitize(),
	)); err != nil {
		t.Fatalf("provision N8 roles: %v", err)
	}
	if _, err := owner.Exec(ctx, `insert into pgbase_meta.admins
		(id,email,password_hash,active,created_at,updated_at)
		values($1,$2,'n8-test',true,current_timestamp,current_timestamp)`,
		actorID, actorID.String()+"@example.com"); err != nil {
		t.Fatalf("provision N8 administrator: %v", err)
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
	catalogVersion := &atomic.Int64{}
	catalogVersion.Store(catalog.Version)
	return poolFixture{
		owner: owner, compilerRole: compilerRole, readRole: readRole, writerRole: writerRole, actorID: actorID,
		catalogVersion: catalogVersion,
		options: PoolOptions{
			CompilerURL:    roleURL(parsed, compilerRole, password),
			ReadURL:        roleURL(parsed, readRole, password),
			WriterURL:      roleURL(parsed, writerRole, password),
			Catalog:        catalog,
			CatalogVersion: func(context.Context) (int64, error) { return catalogVersion.Load(), nil },
			WriteTargets:   []WriteTarget{{Schema: "ext", Table: "items", Columns: []string{"id", "amount", "label"}}},
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
