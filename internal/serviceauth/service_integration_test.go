//go:build integration

package serviceauth

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestServiceTokenLifecycleAndScopeLock(t *testing.T) {
	ctx := context.Background()
	url := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection := uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,'service_scope','service_scope','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, collection); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, []byte("0123456789abcdef0123456789abcdef"))
	issued, err := service.Issue(ctx, "worker", time.Hour, []Scope{{collection, "read"}})
	if err != nil {
		t.Fatal(err)
	}
	if issued.RawToken == "" {
		t.Fatal("missing raw token")
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `select token_hash from pgbase_meta.service_tokens where id=$1`, issued.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == issued.RawToken {
		t.Fatal("raw token stored")
	}
	principal, err := service.Authenticate(ctx, issued.RawToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return service.Authorize(ctx, tx, principal.Subject, collection, "read") }); err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return service.Authorize(ctx, tx, principal.Subject, collection, "delete") }); err != ErrInvalidToken {
		t.Fatalf("wrong scope=%v", err)
	}
	listed, err := service.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Scopes[0].Action != "read" {
		t.Fatalf("list=%#v %v", listed, err)
	}
	if err := service.Revoke(ctx, issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, issued.RawToken); err != ErrInvalidToken {
		t.Fatalf("revoked auth=%v", err)
	}
}

func TestAuthorizeSerializesRevocation(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection := uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,'lock_scope','lock_scope','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, collection); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, []byte("0123456789abcdef0123456789abcdef"))
	issued, err := service.Issue(ctx, "locker", time.Hour, []Scope{{collection, "read"}})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, issued.RawToken)
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	authDone := make(chan error, 1)
	go func() {
		authDone <- pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := service.Authorize(ctx, tx, principal.Subject, collection, "read"); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	revokeDone := make(chan error, 1)
	go func() { revokeDone <- service.Revoke(ctx, issued.ID) }()
	select {
	case err := <-revokeDone:
		t.Fatalf("revoke did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-authDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-revokeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoke remained blocked")
	}
}

func TestListDoesNotTruncateScopesAtJoinedRowLimit(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	scopes := make([]Scope, 256)
	for i := range scopes {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) values($1,$2,$2,'active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, fmt.Sprintf("scope_%03d", i)); err != nil {
			t.Fatal(err)
		}
		scopes[i] = Scope{CollectionID: id, Action: "read"}
	}
	service := NewService(pool, []byte("0123456789abcdef0123456789abcdef"))
	for i := 0; i < 5; i++ {
		if _, err := service.Issue(ctx, fmt.Sprintf("worker-%d", i), time.Hour, scopes); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 5 {
		t.Fatalf("listed tokens = %d, want 5", len(listed))
	}
	for _, token := range listed {
		if len(token.Scopes) != len(scopes) {
			t.Fatalf("token %s scopes = %d, want %d", token.Name, len(token.Scopes), len(scopes))
		}
	}
}
