//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthRecordAutoDateOnSystemUpdate(t *testing.T) {
	ctx := context.Background()
	s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	realm := uuid.New()
	fields := []schema.FieldInput{{Name: "touched", Type: schema.TypeAutoDate, Options: &schema.FieldOptions{OnCreate: true, OnUpdate: true}}}
	if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "review_" + realm.String()[:8], Fields: fields}); err != nil {
		t.Fatal(err)
	}
	record, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "before@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	email := "after@example.test"
	updated, err := s.UpdateAuthRecord(ctx, realm, record.ID, UpdateAuthRecordInput{Email: &email})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Fields["touched"] == record.Fields["touched"] {
		t.Fatalf("email update left OnUpdate timestamp unchanged: %v", updated.Fields["touched"])
	}
}

func TestCreateAuthRecordMissingCollection(t *testing.T) {
	s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	_, err := s.CreateAuthRecord(context.Background(), uuid.New(), CreateAuthRecordInput{Email: "missing@example.test", Password: "correct horse battery staple", Active: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing collection = %v, want not found", err)
	}
}

func TestCreateAuthRecordRechecksContractAfterKDF(t *testing.T) {
	for _, change := range []string{"schema", "password-policy"} {
		t.Run(change, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
			realm := uuid.New()
			if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "review_" + realm.String()[:8], Fields: []schema.FieldInput{{Name: "level", Type: schema.TypeText}}}); err != nil {
				t.Fatal(err)
			}
			paused := closurePausingPasswords{make(chan struct{}), make(chan struct{}), true}
			s.passwords = paused
			result := make(chan error, 1)
			go func() {
				_, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "stale@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"level": "not an integer"}})
				result <- err
			}()
			select {
			case <-paused.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var err error
			if change == "schema" {
				_, err = s.UpdateAuthCollection(ctx, realm, UpdateAuthCollectionInput{Fields: []schema.FieldInput{{Name: "level", Type: schema.TypeInteger}}})
			} else {
				var administration AuthAdministration
				administration, err = s.GetAuthAdministration(ctx, realm)
				if err == nil {
					policy := administration.Policy
					policy.MinimumPasswordBytes = 40
					_, err = s.SetAuthPolicy(ctx, realm, policy)
				}
			}
			close(paused.release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, ErrValidation) {
				t.Fatalf("auth record creation after %s change returned %v, want validation error", change, err)
			}
		})
	}
}

func TestAuthCascadeTraversesTransitivePolicies(t *testing.T) {
	for _, mode := range []string{"restrict", "cascade", "denied", "set-null"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
			createRealm := func(name, target, rule string, action schema.RelationDeleteAction) uuid.UUID {
				t.Helper()
				id := uuid.New()
				var fields []schema.FieldInput
				if target != "" {
					fields = []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: target, Cardinality: schema.RelationSingle, OnDelete: action}}}
				}
				if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: name, Fields: fields, Rules: AuthCollectionRules{Delete: rule}}); err != nil {
					t.Fatal(err)
				}
				return id
			}
			rootRealm := createRealm("review_roots", "", "true", "")
			midRealm := createRealm("review_mids", "review_roots", "true", schema.RelationCascade)
			action, rule := schema.RelationCascade, "true"
			if mode == "restrict" {
				action = schema.RelationRestrict
			}
			if mode == "set-null" {
				action = schema.RelationSetNull
			}
			if mode == "denied" {
				rule = "false"
			}
			leafRealm := createRealm("review_leaves", "review_mids", rule, action)
			create := func(realm uuid.UUID, email string, parent uuid.UUID) AuthRecord {
				t.Helper()
				var fields map[string]any
				if parent != uuid.Nil {
					fields = map[string]any{"parent": parent.String()}
				}
				record, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: email, Password: "correct horse battery staple", Active: true, Fields: fields})
				if err != nil {
					t.Fatal(err)
				}
				return record
			}
			root := create(rootRealm, "root@example.test", uuid.Nil)
			mid := create(midRealm, "mid@example.test", root.ID)
			leaf := create(leafRealm, "leaf@example.test", mid.ID)
			err := s.DeleteAuthorizedAuthRecord(ctx, rootRealm, root.ID, root.ID)
			var want error
			if mode == "restrict" {
				want = ErrConflict
			}
			if mode == "denied" {
				want = ErrInvalidCredentials
			}
			if !errors.Is(err, want) {
				t.Fatalf("delete returned %v, want %v", err, want)
			}
			for _, record := range []AuthRecord{root, mid, leaf} {
				got, err := s.GetAuthRecord(ctx, record.AuthCollectionID, record.ID)
				survives := want != nil || mode == "set-null" && record.ID == leaf.ID
				if survives && err != nil {
					t.Fatalf("surviving record %s unavailable: %v", record.Email, err)
				}
				if !survives && !errors.Is(err, ErrNotFound) {
					t.Fatalf("cascaded record %s retained: %v", record.Email, err)
				}
				if survives && want == nil && got.Fields["parent"] != nil {
					t.Fatal("transitive set-null retained reference")
				}
			}
		})
	}
}

func TestAuthRelationCreateCannotRaceDeletedTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	realm := uuid.New()
	name := "relation_race_" + realm.String()[:8]
	fields := []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: name, Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict}}}
	if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: name, Fields: fields}); err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "race-root@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create function pgbase_meta.pause_auth_delete() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(91386011); return old; end $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create trigger pause_auth_delete before delete on pgbase_meta.users for each row execute function pgbase_meta.pause_auth_delete()`); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `select pg_advisory_xact_lock(91386011)`); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- s.DeleteAuthRecord(ctx, realm, root.ID) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event='advisory' and query like 'delete from pgbase_meta.users%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-deleted:
			t.Fatalf("delete exited before pause: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	created := make(chan error, 1)
	go func() {
		_, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "race-child@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"parent": root.ID.String()}})
		created <- err
	}()
	// Wait for either the old successful create or the corrected target-row lock.
	for {
		select {
		case err := <-created:
			t.Fatalf("create escaped locked target deletion: %v", err)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%for key share of u%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-created; !errors.Is(err, ErrValidation) {
		t.Fatalf("create after target deletion = %v, want validation error", err)
	}
}

func TestAuthRelationDeletionSeedsCompleteRootSet(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	realm := uuid.New()
	name := "multi_roots_" + realm.String()[:8]
	fields := []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: name, Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict}}}
	if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: name, Fields: fields}); err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "first@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "second@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"parent": first.ID.String()}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	roots := []RelationDeleteTarget{{Collection: name, ID: first.ID}, {Collection: name, ID: second.ID}, {Collection: name, ID: first.ID}}
	if err := EnforceRelationDeletes(ctx, tx, roots, nil); err != nil {
		t.Fatalf("restriction between caller-owned deleted roots blocked closure: %v", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.users where auth_collection_id=$1`, realm).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("enforcement removed caller-owned roots: remaining=%d", count)
	}
}

func TestAuthDeleteAuthorizationRejectsNonUsers(t *testing.T) {
	for _, principal := range []identity.Subject{{Kind: identity.Anonymous}, {Kind: identity.Service, ID: uuid.New()}, {Kind: identity.Administrator, ID: uuid.New()}, {Kind: identity.User}} {
		if err := AuthorizeAuthRecordDeleteInTx(context.Background(), nil, principal, uuid.New(), uuid.New()); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("principal %s returned %v, want denied", principal.Kind, err)
		}
	}
}

func TestUnrelatedRelationDeletionDoesNotLockAuthProfiles(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	record, err := s.CreateAuthRecord(ctx, DefaultCollectionID, CreateAuthRecordInput{Email: "unrelated@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `select user_id from pgbase_meta.user_auth_profiles where user_id=$1 for update`, record.ID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tx, err := pool.Begin(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := EnforceRelationDeletes(bounded, tx, []RelationDeleteTarget{{Collection: "unrelated_native", ID: uuid.New()}}, nil); err != nil {
		t.Fatalf("unrelated deletion waited on auth profile lock: %v", err)
	}
}
