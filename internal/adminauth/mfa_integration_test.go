//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestN3UpsertDeleteProtectsLastAdministratorAndInvalidatesSessions(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, NewArgon2idPasswords(), []byte("01234567890123456789012345678901"), "", time.Hour, nil)
	first, created, err := service.UpsertAdministrator(ctx, "ADMIN@example.test", "first correct horse battery staple")
	if err != nil || !created {
		t.Fatalf("first=%#v created=%t err=%v", first, created, err)
	}
	issued, err := service.Login(ctx, first.Email, "first correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	updated, created, err := service.UpsertAdministrator(ctx, first.Email, "second correct horse battery staple")
	if err != nil || created || updated.ID != first.ID {
		t.Fatalf("updated=%#v created=%t err=%v", updated, created, err)
	}
	if _, err := service.Authenticate(ctx, issued.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session error=%v", err)
	}
	if _, err := service.Login(ctx, first.Email, "second correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteAdministrator(ctx, first.Email); !errors.Is(err, ErrConflict) {
		t.Fatalf("last administrator delete error=%v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from pgbase_meta.admins where active").Scan(&count); err != nil || count != 1 {
		t.Fatalf("active administrators=%d err=%v", count, err)
	}
	second, created, err := service.UpsertAdministrator(ctx, "second@example.test", "second administrator password phrase")
	if err != nil || !created {
		t.Fatalf("second=%#v created=%t err=%v", second, created, err)
	}
	if err := service.DeleteAdministrator(ctx, first.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, first.Email, "second correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deleted login error=%v", err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from pgbase_meta.admins where active").Scan(&count); err != nil || count != 1 {
		t.Fatalf("active administrators after delete=%d err=%v", count, err)
	}
}
