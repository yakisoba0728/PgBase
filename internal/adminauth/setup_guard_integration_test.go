//go:build integration

package adminauth

import (
	"context"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestSetupGuardSerializesFirstAdministratorSetupAndReleases(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-0123456789abcdef0123456789", time.Hour, nil)
	release, err := service.BeginSetupGuard(ctx, "setup-token-0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(context.Background(), "setup-token-0123456789abcdef0123456789", "admin@example.test", "correct horse battery staple", "127.0.0.1"); err == nil {
		t.Fatal("Setup succeeded while backup setup guard was held")
	}
	release()
	if _, err := service.Setup(context.Background(), "setup-token-0123456789abcdef0123456789", "admin@example.test", "correct horse battery staple", "127.0.0.1"); err != nil {
		t.Fatalf("Setup after guard release: %v", err)
	}
	if _, err := service.BeginSetupGuard(ctx, "setup-token-0123456789abcdef0123456789"); err == nil {
		t.Fatal("guard admitted after first administrator")
	}
}
