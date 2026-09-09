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
	if err := service.EnableTOTP(ctx, first.Email, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := service.AllowIP(ctx, first.Email, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	otpSecret, err := decodeTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	activeSession, err := service.LoginWithOTP(ctx, first.Email, "second correct horse battery staple", "192.0.2.1", testTOTP(otpSecret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update pgbase_meta.admins set active=false where id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	reactivated, created, err := service.UpsertAdministrator(ctx, first.Email, "third correct horse battery staple")
	if err != nil || created || reactivated.ID != first.ID || !reactivated.Active {
		t.Fatalf("reactivated=%#v created=%t err=%v", reactivated, created, err)
	}
	if _, err := service.Authenticate(ctx, activeSession.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reactivated old session error=%v", err)
	}
	if _, err := service.Login(ctx, first.Email, "second correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reactivated old password error=%v", err)
	}
	if _, err := service.Login(ctx, first.Email, "third correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrOTPRequired) {
		t.Fatalf("reactivated OTP-free login error=%v", err)
	}
	if _, err := service.LoginWithOTP(ctx, first.Email, "third correct horse battery staple", "192.0.2.1", testTOTP(otpSecret, time.Now())); err != nil {
		t.Fatalf("reactivated OTP login: %v", err)
	}
	if prefixes, err := service.ListIP(ctx, first.Email); err != nil || len(prefixes) != 1 || prefixes[0] != "192.0.2.0/24" {
		t.Fatalf("reactivated IP policy=%q err=%v", prefixes, err)
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
