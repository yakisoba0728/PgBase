//go:build integration

package recordaccess_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestRequestRulesSaveAndExecute(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) VALUES($1,'request-save@example.test','x',true,now(),now())`, actor); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, nil, nil)
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: "request_save"})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	created, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor))
	if err != nil {
		t.Fatal(err)
	}
	collection := created.Collection
	rule, err := query.ParseAccessRule(`@request.method = 'GET' && @request.query.mode = 'public' && @request.query.mode:length = 6 && @request.headers.accept:length = 0 && @request.auth.email = 'alice'`)
	if err != nil {
		t.Fatal(err)
	}
	rules := accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule}
	service := recordaccess.NewRuleService(pool, schemas)
	if _, err = service.Set(ctx, collection.Name, rules); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := service.Get(ctx, collection.Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		request *accessrule.RequestContext
		want    bool
	}{
		{&accessrule.RequestContext{Method: "GET", Query: map[string][]string{"mode": {"public"}}, Auth: map[string]any{"email": "alice"}}, true},
		{&accessrule.RequestContext{Method: "POST", Query: map[string][]string{"mode": {"public"}}, Auth: map[string]any{"email": "alice"}}, false},
	} {
		sql, args, e := accessrule.CompilePolicy(loaded.Rules.View, accessrule.PolicyContext{Catalog: schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}, Collection: collection, Alias: collection.TableName, Principal: identity.Subject{Kind: identity.Anonymous}, Request: test.request})
		if e != nil {
			t.Fatal(e)
		}
		var got bool
		if e = pool.QueryRow(ctx, `SELECT (`+sql+`)`, args...).Scan(&got); e != nil || got != test.want {
			t.Fatalf("allowed=%v want=%v err=%v", got, test.want, e)
		}
	}
	if _, _, err = accessrule.CompilePolicy(loaded.Rules.View, accessrule.PolicyContext{Collection: collection, Alias: collection.TableName}); err == nil {
		t.Fatal("missing execution request accepted")
	}
}
