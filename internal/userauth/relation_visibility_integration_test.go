//go:build integration

package userauth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthorizedAuthResponsesFilterRelationsAfterRootAuthorization(t *testing.T) {
	ctx := context.Background()
	service := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	targetID, sourceID := uuid.New(), uuid.New()
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{
		ID: targetID, Name: "response_targets", RecordID: policy,
		Fields: []schema.FieldInput{{Name: "visible", Type: schema.TypeBoolean}},
		Rules:  AuthCollectionRules{View: "visible = true || @request.auth.email = 'manager@example.test'"},
	}); err != nil {
		t.Fatal(err)
	}
	create := func(collection uuid.UUID, id, email string, fields map[string]any) AuthRecord {
		t.Helper()
		record, err := service.CreateAuthRecord(ctx, collection, CreateAuthRecordInput{ID: id, Email: email, Password: "correct horse battery staple", Active: true, Fields: fields})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	visible := create(targetID, "VisibleAuth", "visible@example.test", map[string]any{"visible": true})
	private := create(targetID, "PrivateAuth", "private@example.test", map[string]any{"visible": false})
	maximum := 4
	fields := []schema.FieldInput{
		{Name: "label", Type: schema.TypeText},
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "response_targets", Cardinality: schema.RelationSingle}},
		{Name: "targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "response_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximum}},
	}
	// Root authorization deliberately depends on a relation that will be
	// withheld from the response. Redaction must not change that evaluation.
	rules := AuthCollectionRules{List: "true", View: "target.email = 'private@example.test'", Update: "target.email = 'private@example.test'", Manage: "false"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: sourceID, Name: "response_sources", Fields: fields, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	source := create(sourceID, "", "source@example.test", map[string]any{"target": private.PublicID, "targets": []any{private.PublicID, visible.PublicID}})
	manager := create(sourceID, "", "manager@example.test", map[string]any{})
	check := func(fields map[string]any) {
		t.Helper()
		if fields["target"] != nil || fmt.Sprint(fields["targets"]) != "[VisibleAuth]" {
			t.Fatalf("relation response = %#v", fields)
		}
	}
	for _, principal := range []uuid.UUID{uuid.Nil, source.ID} {
		got, err := service.GetAuthorizedAuthRecord(ctx, sourceID, principal, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		check(got.Fields)
		listed, err := service.ListAuthorizedAuthRecords(ctx, sourceID, principal, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range listed {
			if item.ID == source.ID {
				check(item.Fields)
				found = true
			}
		}
		if !found {
			t.Fatal("authorized source missing")
		}
	}
	updated, err := service.UpdateAuthorizedAuthRecord(ctx, sourceID, source.ID, source.ID, UpdateAuthRecordInput{Fields: map[string]any{"label": "updated"}})
	if err != nil {
		t.Fatal(err)
	}
	check(updated.Fields)
	profile, err := service.GetProfile(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(profile)
	profile, err = service.SetProfile(ctx, source.ID, map[string]any{"label": "profile"})
	if err != nil {
		t.Fatal(err)
	}
	check(profile)
	// The target's policy sees the source-realm actor, rather than requiring
	// the actor to be a member of the target auth collection.
	managed, err := service.GetAuthorizedAuthRecord(ctx, sourceID, manager.ID, source.ID)
	if err != nil || managed.Fields["target"] != private.PublicID || fmt.Sprint(managed.Fields["targets"]) != "[PrivateAuth VisibleAuth]" {
		t.Fatalf("cross-realm actor policy = %#v, %v", managed.Fields, err)
	}
	trusted, err := service.GetAuthRecord(ctx, sourceID, source.ID)
	if err != nil || trusted.Fields["target"] != private.PublicID {
		t.Fatalf("trusted administrator projection = %#v, %v", trusted.Fields, err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collections set view_rule=null where id=$1`, targetID); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetAuthorizedAuthRecord(ctx, sourceID, source.ID, source.ID)
	if err != nil || got.Fields["target"] != nil || fmt.Sprint(got.Fields["targets"]) != "[]" {
		t.Fatalf("current target policy = %#v, %v", got.Fields, err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collections set view_rule='true' where id=$1`, targetID); err != nil {
		t.Fatal(err)
	}
	rules.Manage = "@request.auth.id = id"
	if _, err := service.UpdateAuthCollection(ctx, sourceID, UpdateAuthCollectionInput{Fields: fields, Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	inactive := false
	deactivated, err := service.UpdateAuthorizedAuthRecord(ctx, sourceID, source.ID, source.ID, UpdateAuthRecordInput{Active: &inactive})
	if err != nil || deactivated.Active || deactivated.Fields["target"] != nil || fmt.Sprint(deactivated.Fields["targets"]) != "[]" {
		t.Fatalf("committed self-deactivation response = %#v, %v", deactivated, err)
	}
}
