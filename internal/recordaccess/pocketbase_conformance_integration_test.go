//go:build integration

package recordaccess_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// These cases are black-box translations of PocketBase v0.40.3 expectations
// in core/record_field_resolver_test.go (multi-match, :each, request fields and
// relations), tools/search/token_functions_test.go (geoDistance/strftime), and
// tools/search/identifier_macros_test.go (UTC time macros). They intentionally
// execute the compiled predicates in PostgreSQL rather than asserting PgBase's
// generated SQL.
func TestPocketBaseRuleConformanceAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}

	authorsID, docsID, relationID := uuid.New(), uuid.New(), uuid.New()
	authors := schema.Collection{
		ID: authorsID, Name: "pb_conformance_authors", TableName: "pb_conformance_authors",
		Fields: []schema.Field{{ID: uuid.New(), Name: "name", ColumnName: "name", Type: schema.TypeText}},
	}
	docs := schema.Collection{
		ID: docsID, Name: "pb_conformance_docs", TableName: "pb_conformance_docs",
		Fields: []schema.Field{
			{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText},
			{ID: uuid.New(), Name: "payload", ColumnName: "payload", Type: schema.TypeJSONB},
			{ID: uuid.New(), Name: "place", ColumnName: "place", Type: schema.TypeGeoPoint},
			{ID: uuid.New(), Name: "occurred", ColumnName: "occurred", Type: schema.TypeTimestampTZ},
			{ID: relationID, Name: "author", ColumnName: "author", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: authorsID, Cardinality: schema.RelationSingle, Storage: schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1}}},
		},
	}
	for _, collection := range []schema.Collection{authors, docs} {
		if _, err = pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,$2,$3,'active',now(),now())`, collection.ID, collection.Name, collection.TableName); err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`create table app.pb_conformance_authors(id uuid primary key,name text,created_at timestamptz default now(),updated_at timestamptz default now())`,
		`create table app.pb_conformance_docs(id uuid primary key,title text,payload jsonb,place jsonb,occurred timestamptz,author uuid,created_at timestamptz default now(),updated_at timestamptz default now())`,
	} {
		if _, err = pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	alice, bob, noDocs := uuid.New(), uuid.New(), uuid.New()
	matching, nonmatching := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `insert into app.pb_conformance_authors(id,name) values($1,'Alice'),($2,'Bob'),($3,'Nobody')`, alice, bob, noDocs); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into app.pb_conformance_docs(id,title,payload,place,occurred,author) values
		($1,' Alpha ', '{"tags":["red","blue"],"items":[{"name":"first"}],"nullable":null}', '{"lon":127.0,"lat":37.5}', '2024-02-15T12:30:00Z', $3),
		($2,'Beta',    '{"tags":[],"items":[],"nullable":null}',                         '{"lon":128.0,"lat":38.0}', '2023-01-01T00:00:00Z', $4)`, matching, nonmatching, alice, bob); err != nil {
		t.Fatal(err)
	}
	catalog := schema.Catalog{
		Collections: map[string]schema.Collection{authors.Name: authors, docs.Name: docs},
		Incoming:    map[uuid.UUID][]schema.RelationRef{authorsID: {{SourceCollectionID: docsID, SourceFieldID: relationID}}},
	}
	setRule := func(t *testing.T, collection schema.Collection, source string) {
		t.Helper()
		rule, parseErr := query.ParsePocketBaseAccessRule(source)
		if parseErr != nil {
			t.Fatalf("parse %q: %v", source, parseErr)
		}
		raw, marshalErr := json.Marshal(accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, writeErr := pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2) on conflict(collection_id) do update set rules=excluded.rules`, collection.ID, raw); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	assertRule := func(t *testing.T, collection schema.Collection, source string, request *accessrule.RequestContext, wants map[uuid.UUID]bool) {
		t.Helper()
		setRule(t, collection, source)
		err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			for id, want := range wants {
				got, evalErr := recordaccess.AllowsRecordInCatalog(ctx, tx, catalog, collection, identity.Subject{Kind: identity.User, ID: alice}, "view", id, false, request)
				if evalErr != nil {
					return evalErr
				}
				if got != want {
					t.Errorf("rule %q row %s allowed=%v want=%v", source, id, got, want)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("evaluate %q: %v", source, err)
		}
	}

	t.Run("json indexes arrays modifiers and null", func(t *testing.T) {
		assertRule(t, docs, `payload.items.0.name = 'first' && payload.tags:each ?= 'red' && payload.nullable = null`, nil, map[uuid.UUID]bool{matching: true, nonmatching: false})
		// PocketBase's regular multi comparison is universal, while the ?
		// operator is existential. The empty array therefore satisfies the
		// regular negated comparison and does not satisfy the ? comparison.
		assertRule(t, docs, `payload.tags:each != 'red'`, nil, map[uuid.UUID]bool{matching: false, nonmatching: true})
		assertRule(t, docs, `payload.tags:each ?!= 'red'`, nil, map[uuid.UUID]bool{matching: true, nonmatching: false})
	})

	t.Run("forward backrelation and collection", func(t *testing.T) {
		assertRule(t, docs, `author.name = 'Alice' && @collection.pb_conformance_authors.name ?= 'Bob'`, nil, map[uuid.UUID]bool{matching: true, nonmatching: false})
		assertRule(t, authors, `pb_conformance_docs_via_author.title:lower ~ 'alpha'`, nil, map[uuid.UUID]bool{alice: true, bob: false, noDocs: true})
		assertRule(t, authors, `pb_conformance_docs_via_author.title:lower ?~ 'alpha'`, nil, map[uuid.UUID]bool{alice: true, bob: false, noDocs: false})
	})

	t.Run("functions and utc modifiers", func(t *testing.T) {
		assertRule(t, docs, `geoDistance(place.lon, place.lat, 127, 37.5) < 1`, nil, map[uuid.UUID]bool{matching: true, nonmatching: false})
		assertRule(t, docs, `strftime('%Y-%m-%d', occurred, 'start of month', '+1 day') = '2024-02-02'`, nil, map[uuid.UUID]bool{matching: true, nonmatching: false})
	})

	t.Run("request sources isset and changed", func(t *testing.T) {
		request := &accessrule.RequestContext{
			Present: map[string]bool{"title": true}, Changed: map[string]bool{"title": false}, Method: "PATCH",
			Body:  map[string]json.RawMessage{"title": json.RawMessage(`" Alpha "`)},
			Query: map[string][]string{"mode": {"safe"}}, Headers: map[string][]string{"X_Mode": {"strict"}},
			Auth: map[string]any{"id": alice, "email": "alice@example.test", "verified": true}, Context: map[string]any{"source": "test"},
		}
		assertRule(t, docs, `@request.method = 'PATCH' && @request.auth.email = 'alice@example.test' && @request.auth.verified = true && @request.headers.X_Mode = 'strict' && @request.query.mode = 'safe' && @request.context.source = 'test' && @request.body.title:isset = true && @request.body.title:changed = false`, request, map[uuid.UUID]bool{matching: true, nonmatching: true})
		assertRule(t, docs, `@request.body.title = title`, request, map[uuid.UUID]bool{matching: true, nonmatching: false})
		request.Changed["title"] = true
		assertRule(t, docs, `@request.body.title:isset = true && @request.body.title:changed = false`, request, map[uuid.UUID]bool{matching: false, nonmatching: false})
	})
}
