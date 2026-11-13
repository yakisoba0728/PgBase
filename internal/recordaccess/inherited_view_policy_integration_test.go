//go:build integration

package recordaccess_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestDeclarativeViewPolicyWritesFailAndReadsInherit(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) VALUES($1,'view-policy@example.test','x',true,now(),now())`, actor); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, nil, nil)
	for _, input := range []schema.CreateCollectionInput{{Name: "view_policy_source", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}, {Name: "view_policy_inherited", View: &schema.ViewInput{Source: "view_policy_source", Projection: []string{"title"}}}, {Name: "view_policy_sql", View: &schema.ViewInput{SQL: `SELECT id,title FROM app.view_policy_source`}}} {
		change := schema.NewCreateChange(input)
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor)); err != nil {
			t.Fatal(err)
		}
	}
	access := recordaccess.NewService(pool)
	rules := recordaccess.NewRuleService(pool, schemas)
	allow := accessrule.Rule{Op: "allow"}
	deny := accessrule.Rule{Op: "deny"}
	allowed := accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow}
	denied := accessrule.Rules{List: deny, View: deny, Create: deny, Update: deny, Delete: deny}
	for _, name := range []string{"view_policy_source", "view_policy_sql"} {
		if _, err := access.Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		if _, err := rules.Set(ctx, name, allowed); err != nil {
			t.Fatal(err)
		}
	}
	for _, enabled := range []bool{false, true} {
		if _, err := access.Set(ctx, "view_policy_inherited", enabled); !errors.Is(err, recordaccess.ErrUnsupported) {
			t.Fatalf("set gate %v: %v", enabled, err)
		}
	}
	for _, policy := range []accessrule.Rules{allowed, denied} {
		if _, err := rules.Set(ctx, "view_policy_inherited", policy); !errors.Is(err, recordaccess.ErrUnsupported) {
			t.Fatalf("set rules: %v", err)
		}
	}
	inherited, err := access.Get(ctx, "view_policy_inherited")
	if err != nil || !inherited.Enabled || inherited.Collection != "view_policy_inherited" {
		t.Fatalf("gate=%+v err=%v", inherited, err)
	}
	policy, err := rules.Get(ctx, "view_policy_inherited")
	if err != nil || policy.Rules.View.Op != "allow" || policy.Revision != 1 {
		t.Fatalf("rules=%+v err=%v", policy, err)
	}
	var writes int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.collection_rules WHERE collection_id=c.id)+(SELECT count(*) FROM pgbase_meta.collection_access WHERE collection_id=c.id) FROM pgbase_meta.collections c WHERE name='view_policy_inherited'`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("ignored rows=%d err=%v", writes, err)
	}
	if _, err := access.Set(ctx, "view_policy_source", false); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Set(ctx, "view_policy_source", denied); err != nil {
		t.Fatal(err)
	}
	inherited, err = access.Get(ctx, "view_policy_inherited")
	if err != nil || inherited.Enabled {
		t.Fatalf("source gate not inherited: %+v %v", inherited, err)
	}
	policy, err = rules.Get(ctx, "view_policy_inherited")
	if err != nil || policy.Rules.View.Op != "deny" || policy.Revision != 2 {
		t.Fatalf("source rule not inherited: %+v %v", policy, err)
	}
	sql, err := rules.Get(ctx, "view_policy_sql")
	if err != nil || sql.Rules.View.Op != "allow" {
		t.Fatalf("SQL view lost own policy: %+v %v", sql, err)
	}
}
