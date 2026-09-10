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
