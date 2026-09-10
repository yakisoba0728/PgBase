//go:build integration

package recordaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAuthorizeUserLocksPolicyAndActiveUserUntilTransactionEnds(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collectionID, userID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) VALUES($1,'access_lock_records','access_lock_records','active',transaction_timestamp(),transaction_timestamp())`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,active,created_at,updated_at) VALUES($1,'access-lock@example.test','test',true,transaction_timestamp(),transaction_timestamp())`, userID); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)
	if _, err := service.Set(ctx, "access_lock_records", true); err != nil {
		t.Fatal(err)
	}

	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := AuthorizeUser(ctx, tx, collectionID, userID); err != nil {
			return err
		}
		for _, write := range []struct {
			name string
			run  func(context.Context) error
		}{
			{"policy", func(writeCtx context.Context) error {
				_, err := service.Set(writeCtx, "access_lock_records", false)
				return err
			}},
			{"user", func(writeCtx context.Context) error {
				_, err := pool.Exec(writeCtx, `UPDATE pgbase_meta.users SET active=false WHERE id=$1`, userID)
				return err
			}},
		} {
			t.Run(write.name+" waits", func(t *testing.T) {
				writeCtx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- write.run(writeCtx) }()
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("write committed while authorization lock was held")
					}
				case <-writeCtx.Done():
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(ctx, "access_lock_records", false); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return AuthorizeUser(ctx, tx, collectionID, userID) })
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("authorization after disable = %v", err)
	}
	if _, err := service.Set(ctx, "access_lock_records", true); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		err := AuthorizeUser(canceled, tx, collectionID, userID)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrDenied) {
			t.Fatalf("canceled authorization = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.users SET active=false WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return AuthorizeUser(ctx, tx, collectionID, userID) })
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("authorization after deactivation = %v", err)
	}
}
