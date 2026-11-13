//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
	"time"
)

func TestNestedAuthRelationCannotUseUnrelatedSelfOwner(t *testing.T) {
	pool := newUserAuthPool(t)
	ctx := context.Background()
	var name string
	if err := pool.QueryRow(ctx, `select name from pgbase_meta.auth_collections where id=$1`, DefaultCollectionID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	fields := []schema.FieldInput{
		{Name: "organization", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: name, Cardinality: schema.RelationSingle}},
		{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: name, Cardinality: schema.RelationSingle}},
	}
	raw, _ := json.Marshal(fields)
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=$2 where id=$1`, DefaultCollectionID, raw); err != nil {
		t.Fatal(err)
	}
	root, organization, owner, attacker := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{root, organization, owner, attacker} {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,$3,'unused',true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, DefaultCollectionID, string(rune('a'+i))+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for id, attributes := range map[uuid.UUID]map[string]string{root: {"organization": organization.String()}, organization: {"owner": owner.String()}, attacker: {"owner": attacker.String()}} {
		raw, _ := json.Marshal(attributes)
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,CURRENT_TIMESTAMP)`, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	allowed, err := EvaluateAuthRecordRuleInTx(ctx, pool, DefaultCollectionID, "organization.owner.id = @request.auth.id", AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: attacker}, Current: AuthRecord{ID: root}})
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("unrelated self-owner passed organization.owner authorization")
	}
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	rule := "organization.owner.id = @request.auth.id"
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set list_rule=$2,view_rule=$2,update_rule=$2,manage_rule=$2 where id=$1`, DefaultCollectionID, rule); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []uuid.UUID{attacker, owner} {
		want := actor == owner
		list, err := service.ListAuthorizedAuthRecords(ctx, DefaultCollectionID, actor, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range list {
			found = found || record.ID == root
		}
		if found != want {
			t.Fatalf("list actor=%s found=%v", actor, found)
		}
		_, err = service.GetAuthorizedAuthRecord(ctx, DefaultCollectionID, actor, root)
		if (err == nil) != want {
			t.Fatalf("view allowed=%v want=%v err=%v", err == nil, want, err)
		}
		visibility := true
		_, err = service.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, actor, root, UpdateAuthRecordInput{EmailVisibility: &visibility})
		if (err == nil) != want {
			t.Fatalf("update allowed=%v want=%v err=%v", err == nil, want, err)
		}
		verified := true
		_, err = service.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, actor, root, UpdateAuthRecordInput{Verified: &verified})
		if (err == nil) != want {
			t.Fatalf("manage allowed=%v want=%v err=%v", err == nil, want, err)
		}
		// Isolate the manage rule from the update rule.
		if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule='true' where id=$1`, DefaultCollectionID); err != nil {
			t.Fatal(err)
		}
		_, err = service.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, actor, root, UpdateAuthRecordInput{Verified: &verified})
		if (err == nil) != want || err != nil && !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("manage-only allowed=%v want=%v err=%v", err == nil, want, err)
		}
		if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule=$2 where id=$1`, DefaultCollectionID, rule); err != nil {
			t.Fatal(err)
		}
	}

}
