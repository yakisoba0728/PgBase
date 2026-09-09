//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/passwordfield"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestGeneratedFieldsPersistWithoutSecretArtifacts(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	minimum := 4
	createGeneratedFieldsCollection(t, ctx, schemas, principal.ID, "credentials", []schema.FieldInput{
		{Name: "label", Type: schema.TypeText, Required: true},
		{Name: "touched", Type: schema.TypeAutoDate, Indexed: true, Options: &schema.FieldOptions{OnCreate: true, OnUpdate: true}},
		{Name: "visible_password", Type: schema.TypePassword, Required: true, Options: &schema.FieldOptions{MinLength: &minimum}},
		{Name: "hidden_password", Type: schema.TypePassword, Hidden: true},
	})
	plaintext := "swordfish-secret"
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "generated-create", Collection: "credentials",
		IdempotencyKey: "generated-fields-key-0001",
		Input: map[string]json.RawMessage{
			"label": json.RawMessage(`"one"`), "visible_password": jsonStringRaw(plaintext),
			"hidden_password": json.RawMessage(`"hidden-secret"`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Record.Data["visible_password"] != "" || created.Record.Data["hidden_password"] != "" {
		t.Fatalf("create response exposed password: %#v", created.Record.Data)
	}
	id := uuid.MustParse(created.Record.Data["id"].(string))
	var visibleHash, hiddenHash string
	var firstTouched time.Time
	if err := pool.QueryRow(ctx, `select visible_password,hidden_password,touched from app.credentials where id=$1`, id).
		Scan(&visibleHash, &hiddenHash, &firstTouched); err != nil {
		t.Fatal(err)
	}
	if err := passwordfield.Verify(visibleHash, plaintext); err != nil {
		t.Fatalf("stored visible password: %v", err)
	}
	if err := passwordfield.Verify(hiddenHash, "hidden-secret"); err != nil {
		t.Fatalf("stored hidden password: %v", err)
	}
	assertNoStoredSecret(t, pool, id, plaintext, visibleHash, hiddenHash)

	if _, err := pool.Exec(ctx, `create sequence app.generated_retry_once; create function app.generated_retry() returns trigger language plpgsql as $body$
	begin if nextval('app.generated_retry_once')=1 then raise exception 'retry' using errcode='40001'; end if; return new; end $body$;
	create trigger generated_retry before update on app.credentials for each row execute function app.generated_retry()`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	retriedPlaintext := "rotated-after-retry"
	updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "generated-empty-update",
		Collection: "credentials", ID: id, Input: map[string]json.RawMessage{"visible_password": jsonStringRaw(retriedPlaintext)}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Record.Data["visible_password"] != "" || updated.Record.Data["hidden_password"] != "" {
		t.Fatalf("update response exposed password: %#v", updated.Record.Data)
	}
	var secondTouched time.Time
	if err := pool.QueryRow(ctx, `select touched,visible_password from app.credentials where id=$1`, id).Scan(&secondTouched, &visibleHash); err != nil {
		t.Fatal(err)
	}
	if !secondTouched.After(firstTouched) {
		t.Fatalf("empty update touched=%s, want after %s", secondTouched, firstTouched)
	}
	if err := passwordfield.Verify(visibleHash, retriedPlaintext); err != nil {
		t.Fatalf("retried password update was not committed: %v", err)
	}
	assertNoStoredSecret(t, pool, id, retriedPlaintext, visibleHash)

	replayed, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "generated-replay",
		Collection: "credentials", IdempotencyKey: "generated-fields-key-0001",
		Input: map[string]json.RawMessage{"label": json.RawMessage(`"one"`), "visible_password": jsonStringRaw(plaintext), "hidden_password": json.RawMessage(`"hidden-secret"`)}})
	if err != nil || !replayed.Replayed || replayed.Record.Data["visible_password"] != "" {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	_, err = records.Create(ctx, CreateRequest{Principal: principal, RequestID: "generated-conflict",
		Collection: "credentials", IdempotencyKey: "generated-fields-key-0001",
		Input: map[string]json.RawMessage{"label": json.RawMessage(`"one"`), "visible_password": json.RawMessage(`"different-secret"`)}})
	if !IsCode(err, CodeIdempotencyConflict) {
		t.Fatalf("different-body replay error=%v", err)
	}
}

func TestGeneratedFieldsRejectOverridesClearAndRollbackBatch(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createGeneratedFieldsCollection(t, ctx, schemas, principal.ID, "generated_optional", []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true, Unique: true},
		{Name: "created_stamp", Type: schema.TypeAutoDate, Options: &schema.FieldOptions{OnCreate: true}},
		{Name: "changed_stamp", Type: schema.TypeAutoDate, Options: &schema.FieldOptions{OnUpdate: true}},
		{Name: "password", Type: schema.TypePassword},
	})
	_, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "override-autodate", Collection: "generated_optional",
		Input: map[string]json.RawMessage{"name": json.RawMessage(`"override"`), "created_stamp": json.RawMessage(`"2020-01-01T00:00:00Z"`)}})
	if err == nil {
		t.Fatal("client autodate override succeeded")
	}
	results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "generated-batch", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "generated_optional", Input: map[string]json.RawMessage{"name": json.RawMessage(`"rollback"`), "password": json.RawMessage(`"batch-secret"`)}}},
		{Create: &CreateRequest{Collection: "generated_optional", Input: map[string]json.RawMessage{"name": json.RawMessage(`"rollback"`)}}},
	}})
	if err == nil || results != nil {
		t.Fatalf("batch result=%+v error=%v", results, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from app.generated_optional`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed batch rows=%d err=%v", rows, err)
	}
	upsertID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000188")
	createUpsert := &UpdateRequest{Collection: "generated_optional", ID: upsertID,
		Input: map[string]json.RawMessage{"name": json.RawMessage(`"upsert"`), "password": json.RawMessage(`"upsert-secret"`)}}
	if _, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "generated-upsert-create",
		Operations: []BatchOperation{{Upsert: createUpsert}}}); err != nil {
		t.Fatalf("upsert create: %v", err)
	}
	var createdStamp time.Time
	var changedStamp *time.Time
	if err := pool.QueryRow(ctx, `select created_stamp,changed_stamp from app.generated_optional where id=$1`, upsertID).Scan(&createdStamp, &changedStamp); err != nil || changedStamp != nil {
		t.Fatalf("upsert create stamps created=%s changed=%v err=%v", createdStamp, changedStamp, err)
	}
	updateUpsert := &UpdateRequest{Collection: "generated_optional", ID: upsertID, Input: map[string]json.RawMessage{}}
	if _, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "generated-upsert-update",
		Operations: []BatchOperation{{Upsert: updateUpsert}}}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if err := pool.QueryRow(ctx, `select changed_stamp from app.generated_optional where id=$1`, upsertID).Scan(&changedStamp); err != nil || changedStamp == nil {
		t.Fatalf("upsert update changed stamp=%v err=%v", changedStamp, err)
	}

	created, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "optional-create", Collection: "generated_optional",
		Input: map[string]json.RawMessage{"name": json.RawMessage(`"clear"`), "password": json.RawMessage(`"temporary"`)}})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(created.Record.Data["id"].(string))
	if _, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "optional-clear", Collection: "generated_optional", ID: id,
		Input: map[string]json.RawMessage{"password": json.RawMessage(`""`)}}); err != nil {
		t.Fatal(err)
	}
	var cleared *string
	if err := pool.QueryRow(ctx, `select password from app.generated_optional where id=$1`, id).Scan(&cleared); err != nil || cleared != nil {
		t.Fatalf("cleared password=%v err=%v", cleared, err)
	}
}

func createGeneratedFieldsCollection(t *testing.T, ctx context.Context, schemas *schema.Service, actor uuid.UUID, name string, fields []schema.FieldInput) {
	t.Helper()
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: name, Fields: fields})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatalf("preview generated fields collection: %v", err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor)); err != nil {
		t.Fatalf("apply generated fields collection: %v", err)
	}
}

func assertNoStoredSecret(t *testing.T, pool *database.Pool, id uuid.UUID, secrets ...string) {
	t.Helper()
	var artifacts string
	if err := pool.QueryRow(context.Background(), `select coalesce(string_agg(value,''),'') from (
		select response_body::text value from pgbase_meta.record_idempotency where record_id=$1
		union all select envelope::text from pgbase_meta.record_outbox where record_id=$1
		union all select record_snapshot::text from pgbase_meta.record_outbox where record_id=$1) artifacts`, id).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(artifacts, secret) {
			t.Fatalf("stored mutation artifact exposed secret material")
		}
	}
}

func jsonStringRaw(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
