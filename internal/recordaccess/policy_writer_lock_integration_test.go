//go:build integration

package recordaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAbsentPolicyFirstInsertsSerializeOnCollection(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,'policy-lock@example.com','x',true,now(),now())`, actor); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, nil, nil)
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: "policy_lock"})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	created, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`delete from pgbase_meta.collection_rules where collection_id=$1`, `delete from pgbase_meta.collection_access where collection_id=$1`} {
		if _, err := pool.Exec(ctx, query, created.Collection.ID); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select id from pgbase_meta.collections where id=$1 for update`, created.Collection.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { _, err := NewService(pool).Set(ctx, "policy_lock", true); done <- err }()
	go func() {
		_, err := NewRuleService(pool, schemas).Set(ctx, "policy_lock", accessrule.OwnerRules())
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			t.Fatalf("writer escaped common lock: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("policy writer did not finish")
		}
	}
	var accessCount, ruleCount int
	if err := pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.collection_access where collection_id=$1),(select count(*) from pgbase_meta.collection_rules where collection_id=$1)`, created.Collection.ID).Scan(&accessCount, &ruleCount); err != nil {
		t.Fatal(err)
	}
	if accessCount != 1 || ruleCount != 1 {
		t.Fatalf("access=%d rules=%d", accessCount, ruleCount)
	}

	service := NewRuleService(pool, schemas)
	policy, err := service.Get(ctx, "policy_lock")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list", "update"} {
		next := accessrule.OwnerRules()
		if action == "list" {
			next.List = accessrule.Rule{Op: "deny"}
		} else {
			next.Update = accessrule.Rule{Op: "deny"}
		}
		go func() {
			_, err := service.SetAtRevision(ctx, "policy_lock", next, &policy.Revision)
			done <- err
		}()
	}
	var writes, stale int
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			switch {
			case err == nil:
				writes++
			case errors.Is(err, ErrRuleRevisionConflict):
				stale++
			default:
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("conditional rule writer did not finish")
		}
	}
	stored, err := service.Get(ctx, "policy_lock")
	if err != nil || writes != 1 || stale != 1 || stored.Revision != policy.Revision+1 {
		t.Fatalf("writes=%d stale=%d revision=%d err=%v", writes, stale, stored.Revision, err)
	}
}
