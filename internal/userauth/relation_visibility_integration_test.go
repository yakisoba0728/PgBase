//go:build integration

package userauth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestAuthorizedAuthReadsRetrySerializationFailureFromTargetPolicyUpdate holds
// an uncommitted update of the relation target's auth collection row while an
// authorized read waits for its FOR SHARE lock from an older repeatable-read
// snapshot. PostgreSQL aborts that wait with 40001 once the update commits;
// each read must retry against a fresh snapshot instead of failing.
func TestAuthorizedAuthReadsRetrySerializationFailureFromTargetPolicyUpdate(t *testing.T) {
	ctx := context.Background()
	service := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	targetID, sourceID := uuid.New(), uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: targetID, Name: "retry_targets", Rules: AuthCollectionRules{View: "true"}}); err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateAuthRecord(ctx, targetID, CreateAuthRecordInput{Email: "retry-target@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	fields := []schema.FieldInput{{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "retry_targets", Cardinality: schema.RelationSingle}}}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: sourceID, Name: "retry_sources", Fields: fields, Rules: AuthCollectionRules{List: "true", View: "true"}}); err != nil {
		t.Fatal(err)
	}
	source, err := service.CreateAuthRecord(ctx, sourceID, CreateAuthRecordInput{Email: "retry-source@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"target": target.PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() (any, error){
		"ListAuthorizedAuthRecords": func() (any, error) {
			page, err := service.ListAuthorizedAuthRecords(ctx, sourceID, source.ID, 10)
			if err == nil && (len(page) != 1 || page[0].Fields["target"] != target.PublicID) {
				return nil, fmt.Errorf("page = %#v", page)
			}
			return page, err
		},
		"GetAuthorizedAuthRecord": func() (any, error) {
			record, err := service.GetAuthorizedAuthRecord(ctx, sourceID, source.ID, source.ID)
			if err == nil && record.Fields["target"] != target.PublicID {
				return nil, fmt.Errorf("record = %#v", record)
			}
			return record, err
		},
		"GetProfile": func() (any, error) {
			profile, err := service.GetProfile(ctx, source.ID)
			if err == nil && profile["target"] != target.PublicID {
				return nil, fmt.Errorf("profile = %#v", profile)
			}
			return profile, err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			locked, release := make(chan struct{}), make(chan struct{})
			writer := make(chan error, 1)
			go func() {
				writer <- service.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `update pgbase_meta.auth_collections set view_rule='true' where id=$1`, targetID); err != nil {
						return err
					}
					close(locked)
					<-release
					return nil
				})
			}()
			<-locked
			type readResult struct {
				value any
				err   error
			}
			result := make(chan readResult, 1)
			go func() {
				value, err := read()
				result <- readResult{value: value, err: err}
			}()
			waitForTargetPolicyShareWaiter(t, service)
			close(release)
			if err := <-writer; err != nil {
				t.Fatalf("target policy update: %v", err)
			}
			if got := <-result; got.err != nil {
				t.Fatalf("read after concurrent target policy commit = %v, want a retry against a fresh snapshot", got.err)
			}
		})
	}
}

func waitForTargetPolicyShareWaiter(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		if err := service.pool.QueryRow(context.Background(), `select count(*) from pg_stat_activity
			where datname = current_database()
			  and wait_event_type = 'Lock'
			  and query like '%view_rule from pgbase_meta.auth_collections%'
			  and query like '%for share%'`).Scan(&waiters); err != nil {
			t.Fatalf("read target policy lock waiters: %v", err)
		}
		if waiters >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("authorized read did not wait on the target auth collection row")
}
