//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type userAccessFixture struct {
	pool                 *database.Pool
	schemas              *schema.Service
	records              *Service
	admin, first, second Principal
}

func newUserAccessFixture(t *testing.T) userAccessFixture {
	t.Helper()
	pool, schemas, records, admin := newRecordOperationsTest(t)
	ctx := context.Background()
	users := []Principal{{Kind: PrincipalUser, ID: uuid.New()}, {Kind: PrincipalUser, ID: uuid.New()}}
	for index, user := range users {
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,active,created_at,updated_at) VALUES($1,$2,'test',true,transaction_timestamp(),transaction_timestamp())`, user.ID, "owner"+string(rune('a'+index))+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	return userAccessFixture{pool: pool, schemas: schemas, records: records, admin: admin, first: users[0], second: users[1]}
}

func (f userAccessFixture) enable(t *testing.T, collection string) {
	t.Helper()
	if _, err := recordaccess.NewService(f.pool).Set(context.Background(), collection, true); err != nil {
		t.Fatal(err)
	}
}

func (f userAccessFixture) create(t *testing.T, principal Principal, title, key string) MutationResult {
	t.Helper()
	result, err := f.records.Create(context.Background(), CreateRequest{Principal: principal, RequestID: "owner-access-" + uuid.NewString(), Collection: "records", IdempotencyKey: key, Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUserAccessDefaultsLockedAndDoesNotBackfillAdminRecords(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	adminRecord := f.create(t, f.admin, "admin-only", "owner-admin-record-0001")
	_, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "owner-locked-create", Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"locked"`)}})
	if !errors.Is(err, recordaccess.ErrDenied) {
		t.Fatalf("locked create = %v", err)
	}
	f.enable(t, "records")
	page, err := f.records.List(ctx, ListRequest{Principal: f.first, RequestID: "owner-list-admin", Collection: "records", Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("user list admin records = %#v, %v", page, err)
	}
	id := uuid.MustParse(adminRecord.Record.Data["id"].(string))
	_, err = f.records.Get(ctx, GetRequest{Principal: f.first, RequestID: "owner-get-admin", Collection: "records", ID: id})
	if !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("user get admin record = %v", err)
	}
	_, err = f.records.Update(ctx, UpdateRequest{Principal: f.first, RequestID: "owner-update-admin", Collection: "records", ID: id, Input: map[string]json.RawMessage{"title": json.RawMessage(`"intrusion"`)}})
	if !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("user update admin record = %v", err)
	}
}

func TestUserCreateOwnershipReplayCursorAndRollback(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	f.enable(t, "records")
	first := f.create(t, f.first, "one", "owner-replay-key-0001")
	firstID := uuid.MustParse(first.Record.Data["id"].(string))
	var owner uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT user_id FROM pgbase_meta.record_owners WHERE record_id=$1`, firstID).Scan(&owner); err != nil || owner != f.first.ID {
		t.Fatalf("committed owner = %s, %v", owner, err)
	}
	replay, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "owner-replay-request", Collection: "records", IdempotencyKey: "owner-replay-key-0001", Input: map[string]json.RawMessage{"title": json.RawMessage(`"one"`)}})
	if err != nil || !replay.Replayed || replay.Record.Data["id"] != first.Record.Data["id"] {
		t.Fatalf("same-user replay = %#v, %v", replay, err)
	}
	if _, err := recordaccess.NewService(f.pool).Set(ctx, "records", false); err != nil {
		t.Fatal(err)
	}
	_, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "owner-disabled-replay", Collection: "records", IdempotencyKey: "owner-replay-key-0001", Input: map[string]json.RawMessage{"title": json.RawMessage(`"one"`)}})
	if !errors.Is(err, recordaccess.ErrDenied) {
		t.Fatalf("disabled replay = %v", err)
	}
	f.enable(t, "records")
	second := f.create(t, f.first, "two", "owner-second-key-0001")
	other := f.create(t, f.second, "other", "owner-replay-key-0001")
	if other.Record.Data["id"] == first.Record.Data["id"] {
		t.Fatal("idempotency key crossed user boundary")
	}
	page, err := f.records.List(ctx, ListRequest{Principal: f.first, RequestID: "owner-cursor-first", Collection: "records", Limit: 1, Sort: "title"})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("owner cursor page = %#v, %v", page, err)
	}
	_, err = f.records.List(ctx, ListRequest{Principal: f.second, RequestID: "owner-cursor-other", Collection: "records", Limit: 1, Sort: "title", Cursor: page.NextCursor})
	if !query.IsInvalidCursor(err) {
		t.Fatalf("cross-user cursor = %v", err)
	}
	var beforeOwners, beforeRevisions, beforeAudits, beforeOutbox int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.record_owners),(SELECT count(*) FROM pgbase_meta.record_revisions),(SELECT count(*) FROM pgbase_meta.record_audit_events),(SELECT count(*) FROM pgbase_meta.record_outbox)`).Scan(&beforeOwners, &beforeRevisions, &beforeAudits, &beforeOutbox); err != nil {
		t.Fatal(err)
	}
	_, err = f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "owner-rollback-create", Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"one"`)}})
	if err == nil {
		t.Fatal("duplicate create succeeded")
	}
	var owners, revisions, audits, outbox int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.record_owners),(SELECT count(*) FROM pgbase_meta.record_revisions),(SELECT count(*) FROM pgbase_meta.record_audit_events),(SELECT count(*) FROM pgbase_meta.record_outbox)`).Scan(&owners, &revisions, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if owners != beforeOwners || revisions != beforeRevisions || audits != beforeAudits || outbox != beforeOutbox {
		t.Fatalf("failed create leaked artifacts: %d/%d/%d/%d -> %d/%d/%d/%d", beforeOwners, beforeRevisions, beforeAudits, beforeOutbox, owners, revisions, audits, outbox)
	}
	_ = second
}

func TestUserAccessRejectsOutgoingAndIncomingRelations(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	apply := func(input schema.CreateCollectionInput) {
		plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
			t.Fatal(err)
		}
	}
	apply(schema.CreateCollectionInput{Name: "owner_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}}})
	apply(schema.CreateCollectionInput{Name: "owner_sources", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}, {Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "owner_targets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationCascade}}}})
	f.enable(t, "owner_targets")
	f.enable(t, "owner_sources")
	for _, collection := range []string{"owner_targets", "owner_sources"} {
		_, err := f.records.Create(ctx, CreateRequest{Principal: f.first, RequestID: "owner-relation-" + collection, Collection: collection, Input: map[string]json.RawMessage{"name": json.RawMessage(`"blocked"`)}})
		if !errors.Is(err, recordaccess.ErrUnsupported) {
			t.Fatalf("user create %s = %v", collection, err)
		}
	}
}
