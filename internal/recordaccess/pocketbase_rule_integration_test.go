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

func TestPocketBaseScalarRulesRoundTripAndEnforceRows(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection := schema.Collection{ID: uuid.New(), Name: "pb_rule_notes", TableName: "pb_rule_notes", Fields: []schema.Field{{ID: uuid.New(), Name: "title", ColumnName: "title", Type: schema.TypeText}, {ID: uuid.New(), Name: "score", ColumnName: "score", Type: schema.TypeInteger}, {ID: uuid.New(), Name: "author", ColumnName: "author", Type: schema.TypeUUID}}}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,$2,$3,'active',now(),now())`, collection.ID, collection.Name, collection.TableName); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create table app.pb_rule_notes(id uuid primary key,title text,score integer,author uuid,created_at timestamptz default now(),updated_at timestamptz default now())`); err != nil {
		t.Fatal(err)
	}
	allowedID, emptyID, nullID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `insert into app.pb_rule_notes(id,title,score,author) values($1,'hello',3,$4),($2,'',3,$4),($3,null,3,$4)`, allowedID, emptyID, nullID, userID); err != nil {
		t.Fatal(err)
	}
	source := "title != null && title != '' && score >= 2 && author = @request.auth.id"
	rule, err := query.ParsePocketBaseAccessRule(source)
	if err != nil {
		t.Fatal(err)
	}
	rules := accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule}
	raw, _ := json.Marshal(rules)
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2)`, collection.ID, raw); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		principal := identity.Subject{Kind: identity.User, ID: userID}
		for id, want := range map[uuid.UUID]bool{allowedID: true, emptyID: false, nullID: false} {
			got, checkErr := recordaccess.AllowsRecord(ctx, tx, collection, principal, "view", id, false)
			if checkErr != nil {
				return checkErr
			}
			if got != want {
				t.Fatalf("id=%s allowed=%v want=%v", id, got, want)
			}
		}
		loaded, _, loadErr := recordaccess.LoadRules(ctx, tx, collection.ID)
		if loadErr != nil {
			return loadErr
		}
		if got := loaded.PocketBase().View; got == nil || *got != source {
			t.Fatalf("roundtrip=%v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPocketBaseCatalogRulesEnforceRelationsBackrelationsCollectionsAndJSON(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	authorID, docsID, flagsID, relationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	authors := schema.Collection{ID: authorID, Name: "pb_authors", TableName: "pb_authors", Fields: []schema.Field{{ID: uuid.New(), Name: "name", ColumnName: "name", Type: schema.TypeText}}}
	docs := schema.Collection{ID: docsID, Name: "pb_docs", TableName: "pb_docs", Fields: []schema.Field{{ID: relationID, Name: "author", ColumnName: "author", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: authorID, Cardinality: schema.RelationSingle, Storage: schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1}}}, {ID: uuid.New(), Name: "payload", ColumnName: "payload", Type: schema.TypeJSONB}}}
	flags := schema.Collection{ID: flagsID, Name: "pb_flags", TableName: "pb_flags", Fields: []schema.Field{{ID: uuid.New(), Name: "name", ColumnName: "name", Type: schema.TypeText}}}
	for _, c := range []schema.Collection{authors, docs, flags} {
		if _, err = pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,$2,$3,'active',now(),now())`, c.ID, c.Name, c.TableName); err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{`create table app.pb_authors(id uuid primary key,name text,created_at timestamptz default now(),updated_at timestamptz default now())`, `create table app.pb_docs(id uuid primary key,author uuid,payload jsonb,created_at timestamptz default now(),updated_at timestamptz default now())`, `create table app.pb_flags(id uuid primary key,name text,created_at timestamptz default now(),updated_at timestamptz default now())`} {
		if _, err = pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	alice, bob, good, bad := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `insert into app.pb_authors values($1,'Alice',now(),now()),($2,'Bob',now(),now())`, alice, bob)
	if err == nil {
		_, err = pool.Exec(ctx, `insert into app.pb_docs values($1,$3,'{"level":"gold"}',now(),now()),($2,$4,'{"level":"silver"}',now(),now())`, good, bad, alice, bob)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `insert into app.pb_flags values(gen_random_uuid(),'enabled',now(),now())`)
	}
	if err != nil {
		t.Fatal(err)
	}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{"pb_authors": authors, "pb_docs": docs, "pb_flags": flags}, Incoming: map[uuid.UUID][]schema.RelationRef{authorID: {{SourceCollectionID: docsID, SourceFieldID: relationID}}}}
	setRule := func(c schema.Collection, source string) {
		rule, e := query.ParsePocketBaseAccessRule(source)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule})
		if _, e = pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2) on conflict(collection_id) do update set rules=excluded.rules`, c.ID, raw); e != nil {
			t.Fatal(e)
		}
	}
	setRule(docs, "author.name = 'Alice' && payload.level = 'gold' && @collection.pb_flags.name ?= 'enabled'")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		for id, want := range map[uuid.UUID]bool{good: true, bad: false} {
			got, e := recordaccess.AllowsRecordInCatalog(ctx, tx, catalog, docs, identity.Subject{Kind: identity.Anonymous}, "view", id, false, nil)
			if e != nil {
				return e
			}
			if got != want {
				t.Fatalf("doc %s got=%v", id, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setRule(authors, "pb_docs_via_author.payload.level = 'gold'")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		for id, want := range map[uuid.UUID]bool{alice: true, bob: false} {
			got, e := recordaccess.AllowsRecordInCatalog(ctx, tx, catalog, authors, identity.Subject{Kind: identity.Anonymous}, "view", id, false, nil)
			if e != nil {
				return e
			}
			if got != want {
				t.Fatalf("author %s got=%v", id, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
