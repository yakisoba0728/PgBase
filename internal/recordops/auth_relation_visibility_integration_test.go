//go:build integration

package recordops

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func TestAuthRelationsUseBaseTargetViewAndAdmissionPolicy(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := recordOperationsTestContext(t)
	f.enable(t, "records")
	allow := accessrule.Rule{Op: "allow"}
	view := accessrule.Rule{Op: "eq", Field: "title", Value: json.RawMessage(`"visible"`)}
	if _, err := recordaccess.NewRuleService(f.pool, f.schemas).Set(ctx, "records", accessrule.Rules{List: allow, View: view, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	visible := f.create(t, f.admin, "visible", "auth-target-visible-0001")
	private := f.create(t, f.admin, "private", "auth-target-private-0001")
	service := userauth.NewService(f.pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	realm := uuid.New()
	maximum := 4
	fields := []schema.FieldInput{
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "records", Cardinality: schema.RelationSingle}},
		{Name: "targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "records", Cardinality: schema.RelationMultiple, MaxSelect: &maximum}},
	}
	if _, err := service.CreateAuthCollection(ctx, userauth.CreateAuthCollectionInput{ID: realm, Name: "base_relation_profiles", Fields: fields, Rules: userauth.AuthCollectionRules{List: "true", View: "true"}}); err != nil {
		t.Fatal(err)
	}
	owner, err := service.CreateAuthRecord(ctx, realm, userauth.CreateAuthRecordInput{Email: "base-related@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{
		"target": private.Record.Data["id"], "targets": []any{private.Record.Data["id"], visible.Record.Data["id"]},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, principal := range []uuid.UUID{uuid.Nil, owner.ID} {
		got, err := service.GetAuthorizedAuthRecord(ctx, realm, principal, owner.ID)
		if err != nil || got.Fields["target"] != nil || fmt.Sprint(got.Fields["targets"]) != fmt.Sprint([]any{visible.Record.Data["id"]}) {
			t.Fatalf("base target view = %#v, %v", got.Fields, err)
		}
	}
	if _, err := recordaccess.NewService(f.pool).Set(ctx, "records", false); err != nil {
		t.Fatal(err)
	}
	profile, err := service.GetProfile(ctx, owner.ID)
	if err != nil || profile["target"] != nil || fmt.Sprint(profile["targets"]) != "[]" {
		t.Fatalf("base target admission = %#v, %v", profile, err)
	}
}
