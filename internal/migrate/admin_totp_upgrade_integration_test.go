//go:build integration

package migrate

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAdministratorTOTPCounterUpgradePreservesExistingFactors(t *testing.T) {
	pool, err := database.Open(t.Context(), testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	legacy := New(pool, nil)
	for index, migration := range legacy.migrations {
		if migration.Version == 72 {
			legacy.migrations = legacy.migrations[:index]
			break
		}
	}
	if legacy.migrations[len(legacy.migrations)-1].Version != 71 {
		t.Fatal("fixture must start at schema 71")
	}
	if err := legacy.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	id := uuid.New()
	enabledAt := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	ciphertext := bytes.Repeat([]byte{0x42}, 32)
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,created_at,updated_at)
		values($1,'totp-upgrade@example.test','fixture-password-hash',$2,$2)`, id, enabledAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admin_totp(administrator_id,secret_ciphertext,enabled_at)
		values($1,$2,$3)`, id, ciphertext, enabledAt); err != nil {
		t.Fatal(err)
	}
	runner := New(pool, nil)
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var storedSecret []byte
	var storedEnabledAt time.Time
	var lastCounter *int64
	if err := pool.QueryRow(ctx, `select secret_ciphertext,enabled_at,last_counter from pgbase_meta.admin_totp where administrator_id=$1`, id).Scan(&storedSecret, &storedEnabledAt, &lastCounter); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedSecret, ciphertext) || !storedEnabledAt.Equal(enabledAt) || lastCounter != nil {
		t.Fatal("upgrade changed a factor or assigned an unobserved counter")
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.admin_totp set last_counter=123 where administrator_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select last_counter from pgbase_meta.admin_totp where administrator_id=$1`, id).Scan(&lastCounter); err != nil || lastCounter == nil || *lastCounter != 123 {
		t.Fatalf("repeat migration reset consumption: counter=%v err=%v", lastCounter, err)
	}
	status, err := runner.Status(ctx)
	if err != nil || status.Current != 72 || status.Latest != 72 || status.Dirty || !status.Compatible {
		t.Fatalf("migration status=%+v err=%v", status, err)
	}
}
