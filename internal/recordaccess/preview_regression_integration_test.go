//go:build integration

package recordaccess_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestPreviewSupportsPocketBaseRelationsAndRequestValues(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, nil, nil)
	admin := uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,$2,'x',true,now(),now())`, admin, admin.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	apply := func(input schema.CreateCollectionInput) {
		t.Helper()
		change := schema.NewCreateChange(input)
		plan, previewErr := schemas.Preview(ctx, change)
		if previewErr != nil {
			t.Fatal(previewErr)
		}
		if _, applyErr := schemas.Apply(ctx, schema.LegacyApply(change, plan, admin)); applyErr != nil {
			t.Fatal(applyErr)
		}
	}
	apply(schema.CreateCollectionInput{Name: "preview_authors", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}}})
	apply(schema.CreateCollectionInput{Name: "preview_docs", Fields: []schema.FieldInput{{Name: "author", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "preview_authors", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict}}}})
	authorID, docID, userID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `insert into app.preview_authors(id,name,created_at,updated_at) values($1,'Alice',now(),now())`, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app.preview_docs(id,author,created_at,updated_at) values($1,$2,now(),now())`, docID, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,email,password_hash,active,created_at,updated_at) values($1,$2,'x',true,now(),now())`, userID, userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := recordaccess.NewService(pool).Set(ctx, "preview_docs", true); err != nil {
		t.Fatal(err)
	}
	relationSource := "author.name = 'Alice'"
	relationRule, err := query.ParsePocketBaseAccessRule(relationSource)
	if err != nil {
		t.Fatal(err)
	}
	rules := accessrule.Rules{List: relationRule, View: relationRule, Create: relationRule, Update: relationRule, Delete: relationRule}
	if _, err := recordaccess.NewRuleService(pool, schemas).Set(ctx, "preview_docs", rules); err != nil {
		t.Fatalf("store supported relation rule: %v", err)
	}
	service := recordaccess.NewRuleService(pool, schemas)
	decisions, err := service.Preview(ctx, "preview_docs", recordaccess.RulePreview{Rule: relationRule, Action: "view", UserID: userID, RecordIDs: []uuid.UUID{docID}})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("relation preview=%v err=%v", decisions, err)
	}
	requestRule, err := query.ParsePocketBaseAccessRule("@request.body.author:changed = true")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(authorID.String())
	request := &accessrule.RequestContext{Present: map[string]bool{"author": true}, Changed: map[string]bool{"author": true}, Body: map[string]json.RawMessage{"author": body}}
	decisions, err = service.Preview(ctx, "preview_docs", recordaccess.RulePreview{Rule: requestRule, Action: "update", UserID: userID, RecordIDs: []uuid.UUID{docID}, Request: request})
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("request-value preview=%v err=%v", decisions, err)
	}
}
