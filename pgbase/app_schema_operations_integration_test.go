//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestNewUsesTokenSecretForSchemaOperationHistoryCursors(t *testing.T) {
	ctx := context.Background()
	tokenSecret := []byte("schema-operation-cursor-secret-0001")
	app, err := New(ctx, Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: tokenSecret,
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })

	service, ok := app.schemaOperations.(*schema.Service)
	if !ok {
		t.Fatalf("schema operation runner = %T, want *schema.Service", app.schemaOperations)
	}
	actorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000801")
	if _, err := app.pool.Exec(ctx, `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ($1, 'app-schema-operations@example.com', 'test', true, current_timestamp, current_timestamp)`, actorID); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	for index := range 2 {
		operationID := uuid.MustParse([]string{
			"019551d0-e9c0-7000-8000-000000000811",
			"019551d0-e9c0-7000-8000-000000000812",
		}[index])
		resourceID := uuid.MustParse([]string{
			"019551d0-e9c0-7000-8000-000000000821",
			"019551d0-e9c0-7000-8000-000000000822",
		}[index])
		resourceHash := bytes.Repeat([]byte{byte(index + 1)}, 32)
		idempotencyHash := bytes.Repeat([]byte{byte(index + 3)}, 32)
		if _, err := app.pool.Exec(ctx, `insert into pgbase_meta.schema_operations (
			operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
			resource_key_hash, operation, idempotency_key_hash, request_hash,
			plan_checksum, base_version, status, fence_generation, failure_code,
			created_at, updated_at, completed_at
		) values (
			$1, 'guarded', 'administrator', $2, 'collection', $3,
			$4, 'collection_create', $5, $6,
			$7, 0, 'failed', 1, 'apply_failed',
			$8, $8, $8
		)`, operationID, actorID, resourceID, resourceHash, idempotencyHash,
			bytes.Repeat([]byte{0x21}, 32), bytes.Repeat([]byte{0x31}, 32),
			createdAt.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	firstQuery, err := schema.NewOperationQuery(actorID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := service.ListOperations(ctx, firstQuery)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(firstPage.Items) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %#v, want one item and a continuation cursor", firstPage)
	}

	continuation, err := schema.NewOperationQuery(actorID, 1, firstPage.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	verifier := schema.NewService(app.pool, &schema.Cache{}, schema.Planner{}, nil, tokenSecret)
	secondPage, err := verifier.ListOperations(ctx, continuation)
	if err != nil {
		t.Fatalf("configured token secret did not verify app cursor: %v", err)
	}
	if len(secondPage.Items) != 1 {
		t.Fatalf("second page = %#v, want one item", secondPage)
	}

	wrongVerifier := schema.NewService(app.pool, &schema.Cache{}, schema.Planner{}, nil, bytes.Repeat([]byte{'x'}, 32))
	if _, err := wrongVerifier.ListOperations(ctx, continuation); !errors.Is(err, schema.ErrInvalidOperation) {
		t.Fatalf("cursor verified with a different secret: %v", err)
	}
}
