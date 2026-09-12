//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestUserMigrationSnapshotRejectsInvalidRulesAndReplaysValidRules(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, admin, csrf, "migration_rules")
	ctx := context.Background()
	before, err := fixture.app.UserMigrationSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rollbackFieldID := uuid.New()
	invalid := migrationSnapshotWithRules(t, before, "migration_rules", json.RawMessage(`{"list":{"op":"unsupported"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`), &schema.FieldInput{ID: &rollbackFieldID, Name: "should_rollback", Type: schema.TypeText})
	invalidMigration := UserMigration{
		ID: "202609120430_invalid_rules", Source: []byte("invalid migration rules regression"), BeforeSnapshot: before, AfterSnapshot: invalid,
		Up: func(ctx context.Context, tx pgx.Tx) error {
			return ApplyUserMigrationSnapshot(ctx, tx, before, invalid)
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			return ApplyUserMigrationSnapshot(ctx, tx, invalid, before)
		},
	}
	runner, err := NewUserMigrationRunner(fixture.app, []UserMigration{invalidMigration})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err == nil {
		t.Fatal("migration snapshot committed an unsupported access-rule operator")
	}
	requireN6SnapshotEquivalent(t, fixture.app, before)

	validRules := json.RawMessage(`{"list":{"op":"allow"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
	valid := migrationSnapshotWithRules(t, before, "migration_rules", validRules, nil)
	validMigration := UserMigration{
		ID: "202609120431_valid_rules", Source: []byte("valid migration rules regression"), BeforeSnapshot: before, AfterSnapshot: valid,
		Up:   func(ctx context.Context, tx pgx.Tx) error { return ApplyUserMigrationSnapshot(ctx, tx, before, valid) },
		Down: func(ctx context.Context, tx pgx.Tx) error { return ApplyUserMigrationSnapshot(ctx, tx, valid, before) },
	}
	runner, err = NewUserMigrationRunner(fixture.app, []UserMigration{validMigration})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := runner.Up(ctx); err != nil || len(result) != 1 {
		t.Fatalf("valid rule snapshot Up=%+v error=%v", result, err)
	}
	requireN6SnapshotEquivalent(t, fixture.app, valid)
}

func migrationSnapshotWithRules(t *testing.T, raw []byte, collection string, rules json.RawMessage, addedField *schema.FieldInput) []byte {
	t.Helper()
	var snapshot schema.MigrationSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	for index := range snapshot.Collections {
		if snapshot.Collections[index].Name == collection {
			snapshot.Collections[index].Rules = append(json.RawMessage(nil), rules...)
			if addedField != nil {
				snapshot.Collections[index].Fields = append(snapshot.Collections[index].Fields, *addedField)
			}
			result, err := schema.MarshalMigrationSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("missing migration snapshot collection %q", collection)
	return nil
}
