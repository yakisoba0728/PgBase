//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestClosureRelationRequestAndSurvivorAutoDate(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	apply := func(input schema.CreateCollectionInput) {
		t.Helper()
		plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
			t.Fatal(err)
		}
	}
	apply(schema.CreateCollectionInput{Name: "closure_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}}})
	apply(schema.CreateCollectionInput{Name: "closure_sources", Fields: []schema.FieldInput{{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "closure_targets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}}, {Name: "touched", Type: schema.TypeAutoDate, Options: &schema.FieldOptions{OnCreate: true, OnUpdate: true}}}})
	f.enable(t, "closure_targets")
	f.enable(t, "closure_sources")
	allow := accessrule.Rule{Op: "allow"}
	post := accessrule.Rule{Op: "eq", Field: "__pgbase_request_method", Value: json.RawMessage(`"POST"`)}
	del := accessrule.Rule{Op: "eq", Field: "__pgbase_request_method", Value: json.RawMessage(`"DELETE"`)}
	rules := recordaccess.NewRuleService(f.pool, f.schemas)
	if _, err := rules.Set(ctx, "closure_targets", accessrule.Rules{List: allow, View: post, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Set(ctx, "closure_sources", accessrule.Rules{List: allow, View: allow, Create: allow, Update: del, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	target, err := f.records.Create(ctx, CreateRequest{Principal: f.admin, RequestID: "closure-target", Collection: "closure_targets", Input: map[string]json.RawMessage{"name": json.RawMessage(`"target"`)}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(target.Record.Data["id"])
	input := map[string]json.RawMessage{"target": raw}
	request := &accessrule.RequestContext{Method: "POST"}
	source, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "closure-source", IdempotencyKey: "closure-relation-replay", Collection: "closure_sources", Input: input, RuleContext: request})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "closure-replay", IdempotencyKey: "closure-relation-replay", Collection: "closure_sources", Input: input, RuleContext: request}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "closure-second-source", Collection: "closure_sources", Input: input, RuleContext: request}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `update app.closure_sources set touched='2000-01-01Z' where id=$1`, source.InternalID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.records.Delete(ctx, DeleteRequest{Principal: f.first, RequestID: "closure-delete", Collection: "closure_targets", ID: target.InternalID, RuleContext: &accessrule.RequestContext{Method: "DELETE"}}); err != nil {
		t.Fatal(err)
	}
	var touched time.Time
	if err = f.pool.QueryRow(ctx, `select touched from app.closure_sources where id=$1`, source.InternalID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched.Year() < 2026 {
		t.Fatalf("survivor autodate not updated: %v", touched)
	}
	var updates int
	if err = f.pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_outbox where collection_name='closure_sources' and operation='update' and envelope->'changes' ? 'touched'`).Scan(&updates); err != nil || updates != 2 {
		t.Fatalf("autodate artifacts=%d err=%v", updates, err)
	}

}

func TestClosurePasswordUpdateAuthorizationAndAdmission(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	input := schema.CreateCollectionInput{Name: "closure_password", Fields: []schema.FieldInput{{Name: "pin", Type: schema.TypePassword}}}
	plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
		t.Fatal(err)
	}
	f.enable(t, input.Name)
	seed, err := f.records.Create(ctx, CreateRequest{Principal: f.admin, RequestID: "closure-password-seed", Collection: input.Name, Input: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.records.hashPassword = func(string) (string, error) { calls++; return "$argon2id$fake", nil }
	allow := accessrule.Rule{Op: "allow"}
	deny := accessrule.Rule{Op: "deny"}
	rules := recordaccess.NewRuleService(f.pool, f.schemas)
	for _, nullRule := range []bool{false, true} {
		if _, err = rules.Set(ctx, input.Name, accessrule.Rules{List: allow, View: allow, Create: allow, Update: deny, Delete: allow}); err != nil {
			t.Fatal(err)
		}
		if nullRule {
			if _, err = f.pool.Exec(ctx, `update pgbase_meta.collection_rules set rules=jsonb_set(rules,'{update}','null'::jsonb) where collection_id=(select id from pgbase_meta.collections where name=$1)`, input.Name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "closure-denied-password", Collection: input.Name, ID: seed.InternalID, Input: map[string]json.RawMessage{"pin": json.RawMessage(`"replacement"`)}}); err == nil {
			t.Fatal("denied update succeeded")
		}
		if calls != 0 {
			t.Fatalf("denied update hashed %d times", calls)
		}
	}
	if _, err = rules.Set(ctx, input.Name, accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(f.records.passwordSlots); i++ {
		f.records.passwordSlots <- struct{}{}
	}
	if _, err = f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "closure-bounded-password", Collection: input.Name, ID: seed.InternalID, Input: map[string]json.RawMessage{"pin": json.RawMessage(`"replacement"`)}}); !IsCode(err, CodeUnavailable) {
		t.Fatalf("saturated update=%v", err)
	}
	if calls != 0 {
		t.Fatalf("saturated update hashed %d times", calls)
	}
	for i := 0; i < cap(f.records.passwordSlots); i++ {
		<-f.records.passwordSlots
	}
	if _, err = f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "closure-allowed-password", Collection: input.Name, ID: seed.InternalID, Input: map[string]json.RawMessage{"pin": json.RawMessage(`"replacement"`)}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("allowed hashes=%d", calls)
	}
}

func TestClosureRelationUpdateTakesCollectionBeforeSourceRow(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	input := schema.CreateCollectionInput{Name: "closure_lock", Fields: []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "closure_lock", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}}}}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	created, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, principal.ID))
	if err != nil {
		t.Fatal(err)
	}
	source, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "closure-lock-source", Collection: input.Name, Input: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "closure-lock-target", Collection: input.Name, Input: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(target.Record.Data["id"])
	payload := map[string]json.RawMessage{"parent": raw}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if err = relation.LockMutationCollections(ctx, blocker, created.Collection, payload); err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(ctx)
	var pid int
	if err = writer.QueryRow(ctx, `select pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	outcome := NewTransactionOutcome()
	scope := WithTransactionOutcome(ctx, writer, outcome)
	done := make(chan error, 1)
	go func() {
		_, err := records.Update(scope, UpdateRequest{Principal: principal, RequestID: "closure-lock-update", Collection: input.Name, ID: source.InternalID, Input: payload})
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `select coalesce((select wait_event='advisory' from pg_stat_activity where pid=$1),false)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer never waited on collection")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var id string
	if err = blocker.QueryRow(ctx, `select id::text from app.closure_lock where id=$1 for update nowait`, source.InternalID).Scan(&id); err != nil {
		t.Fatalf("writer locked source before collection: %v", err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	outcome.Finish(ctx, nil)
}
