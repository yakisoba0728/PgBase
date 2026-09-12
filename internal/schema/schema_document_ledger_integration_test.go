//go:build integration

package schema

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSchemaImportConflictingReplayCannotFailForeignReceipt(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	foreign := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,$2,'x',true,now(),now())`, foreign, "foreign@example.com"); err != nil {
		t.Fatal(err)
	}
	operationID := uuid.New()
	idempotency := sha256.Sum256([]byte("original-key-00000001"))
	request := sha256.Sum256([]byte("original-request"))
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.schema_import_operations(operation_id,administrator_id,idempotency_hash,request_hash,base_version,status) values($1,$2,$3,$4,0,'running')`, operationID, actor, idempotency[:], request[:]); err != nil {
		t.Fatal(err)
	}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "hostile", Rules: ownerRuleDocument()}}}
	_, err := service.ApplySchemaImport(context.Background(), SchemaImportIntent{Document: document, BaseVersion: 0, Checksum: strings.Repeat("0", 64), Actor: foreign, OperationID: operationID, IdempotencyKey: "hostile-key-00000001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `select status from pgbase_meta.schema_import_operations where operation_id=$1`, operationID).Scan(&status); err != nil || status != "running" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestSchemaImportStatusDistinguishesLiveAndOrphanedClaims(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	op := uuid.New()
	hash := sha256.Sum256([]byte("claim"))
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.schema_import_operations(operation_id,administrator_id,idempotency_hash,request_hash,base_version,status) values($1,$2,$3,$3,0,'running')`, op, actor, hash[:]); err != nil {
		t.Fatal(err)
	}
	status, err := service.SchemaImportStatus(context.Background(), actor, op)
	if err != nil || status.Status != "unknown" {
		t.Fatalf("orphan status=%+v err=%v", status, err)
	}
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `select pg_advisory_xact_lock($1)`, schemaImportLockKey(op)); err != nil {
		t.Fatal(err)
	}
	status, err = service.SchemaImportStatus(context.Background(), actor, op)
	if err != nil || status.Status != "running" {
		t.Fatalf("live status=%+v err=%v", status, err)
	}
}

func TestCanceledDuplicateCannotFailLiveSchemaImport(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "duplicate_wait", Fields: []FieldInput{{Name: "title", Type: TypeText}}, Rules: ownerRuleDocument()}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	service.beforeDDL = func(context.Context, pgx.Tx) error { once.Do(func() { close(entered) }); <-release; return nil }
	intent := SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "duplicate-wait-00000001"}
	done := make(chan error, 1)
	go func() { _, err := service.ApplySchemaImport(ctx, intent); done <- err }()
	<-entered
	duplicateCtx, cancel := context.WithCancel(ctx)
	duplicateDone := make(chan error, 1)
	go func() { _, err := service.ApplySchemaImport(duplicateCtx, intent); duplicateDone <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-duplicateDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("duplicate error=%v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `select status from pgbase_meta.schema_import_operations where operation_id=$1`, intent.OperationID).Scan(&status); err != nil || status != "running" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTerminatedOperationBackendLeavesRecoverableReceipt(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	op := uuid.New()
	hash := sha256.Sum256([]byte("terminated"))
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.schema_import_operations(operation_id,administrator_id,idempotency_hash,request_hash,base_version,status) values($1,$2,$3,$3,0,'running')`, op, actor, hash[:]); err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := tx.QueryRow(ctx, `select pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, schemaImportLockKey(op)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `select pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	connection.Release()
	status, err := service.SchemaImportStatus(ctx, actor, op)
	if err != nil || status.Status != "unknown" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestSchemaImportLateRuleFailureRollsBackDDLAndPolicies(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "late_failure", Fields: []FieldInput{{Name: "title", Type: TypeText}}, Rules: ownerRuleDocument()}}}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	op := uuid.New()
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: op, IdempotencyKey: "late-failure-00000001", ValidateRules: func(Collection, json.RawMessage) error { return errors.New("late rule failure") }})
	if err == nil {
		t.Fatal("expected failure")
	}
	var table *string
	if err := pool.QueryRow(ctx, `select to_regclass('app.late_failure')::text`).Scan(&table); err != nil || table != nil {
		t.Fatalf("table=%v err=%v", table, err)
	}
	status, err := service.SchemaImportStatus(ctx, actor, op)
	if err != nil || status.Status != "failed" {
		t.Fatalf("receipt=%+v err=%v", status, err)
	}
}

func TestSchemaImportRejectsStalePolicyAndCatalogPreviews(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	base := applyFreshPlan(t, service, CreateCollectionInput{Name: "stale_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, actor)
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "stale_source", UserAccess: true, Rules: ownerRuleDocument()}}}
	policyPlan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_access(collection_id,enabled) values($1,true) on conflict(collection_id) do update set enabled=true`, base.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: policyPlan.BaseVersion, Checksum: policyPlan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "stale-policy-00000001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("policy error=%v", err)
	}
	service = NewService(pool, &Cache{}, nil)
	catalogPlan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	applyFreshPlan(t, service, CreateCollectionInput{Name: "catalog_advanced"}, actor)
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: catalogPlan.BaseVersion, Checksum: catalogPlan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "stale-catalog-00000001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("catalog error=%v", err)
	}
}

func TestSchemaImportRejectsStaleAuthTokenTTLPreview(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	const authID = "00000000-0000-0000-0000-000000000001"
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_token_policies(auth_collection_id,purpose,ttl_seconds,key_version,key_ciphertext,updated_at) values($1,'session',600,'test',decode('00','hex'),clock_timestamp())`, authID); err != nil {
		t.Fatal(err)
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_token_policies set ttl_seconds=601,updated_at=clock_timestamp() where auth_collection_id=$1 and purpose='session'`, authID); err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "stale-auth-ttl-00000001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale auth token TTL error=%v", err)
	}
}

func TestSchemaImportRejectsAuthTokenPurposeInsertedAfterPreview(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	const authID = "00000000-0000-0000-0000-000000000001"
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index := range document.AuthCollections {
		if document.AuthCollections[index].Name == "users" {
			if document.AuthCollections[index].TokenTTLs == nil {
				document.AuthCollections[index].TokenTTLs = map[string]int{}
			}
			document.AuthCollections[index].TokenTTLs["otp"] = 300
		}
	}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_token_policies(auth_collection_id,purpose,ttl_seconds,key_version,key_ciphertext,updated_at) values($1,'otp',301,'test',decode('00','hex'),clock_timestamp())`, authID); err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "inserted-auth-ttl-00000001"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("inserted auth token purpose error=%v", err)
	}
}

func ownerRuleDocument() json.RawMessage {
	return json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
}
