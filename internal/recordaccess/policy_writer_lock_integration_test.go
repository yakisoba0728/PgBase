//go:build integration

package recordaccess

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/accessrule"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
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
}
