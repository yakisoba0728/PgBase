//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthTextRecordIdentityRoundTripsAndPreservesVisibility(t *testing.T) {
	ctx := context.Background()
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	collectionID := uuid.New()
	policy := schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 36, Pattern: `^[A-Za-z0-9-]+$`, AutogeneratePattern: `[a-z0-9]{15}`}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "text_auth_ids", RecordID: &policy}); err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{ID: "AuthCase01", Email: "visible@example.test", EmailVisibility: true, Password: "correct horse battery staple", Active: true})
	if err != nil || record.PublicID != "AuthCase01" || record.ID == uuid.Nil || !record.EmailVisibility {
		t.Fatalf("created auth record = %#v, %v", record, err)
	}
	resolved, err := service.ResolveAuthRecordID(ctx, collectionID, "AuthCase01")
	if err != nil || resolved != record.ID {
		t.Fatalf("resolved auth ID = %s, %v", resolved, err)
	}
	if _, err := service.ResolveAuthRecordID(ctx, collectionID, record.ID.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("internal UUID fallback = %v", err)
	}
	got, err := service.GetAuthRecord(ctx, collectionID, resolved)
	if err != nil || got.PublicID != "AuthCase01" || got.Email != record.Email || !got.EmailVisibility {
		t.Fatalf("loaded auth record = %#v, %v", got, err)
	}
	listed, err := service.ListAuthRecords(ctx, collectionID, 10)
	if err != nil || len(listed) != 1 || listed[0].PublicID != "AuthCase01" {
		t.Fatalf("listed auth records = %#v, %v", listed, err)
	}
	if err := service.DeleteAuthRecord(ctx, collectionID, resolved); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveAuthRecordID(ctx, collectionID, "AuthCase01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted auth identity = %v", err)
	}
}

func TestAuthIdentityScopeRejectsTextCollisionWithUUIDRealm(t *testing.T) {
	ctx := context.Background()
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	native, err := service.CreateAuthRecord(ctx, DefaultCollectionID, CreateAuthRecordInput{Email: "native-id@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	textCollection := uuid.New()
	policy := schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 36, Pattern: `^[a-f0-9-]+$`}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: textCollection, Name: "global_auth_ids", RecordID: &policy}); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateAuthRecord(ctx, textCollection, CreateAuthRecordInput{ID: native.ID.String(), Email: "collision@example.test", Password: "correct horse battery staple", Active: true})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-auth UUID/text collision = %v", err)
	}
}
