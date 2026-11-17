//go:build integration

package apiratelimit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestQuotaDatabaseFailureIsNotReportedAsExhaustion(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	settings := runtimesettings.Default()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/", Duration: 60, MaxRequests: 10}}
	service := New(pool, []byte("01234567890123456789012345678901"), settings)
	admit := func() (Decision, error) {
		return service.Admit(ctx, "GET", "/records", "", "203.0.113.4")
	}
	if decision, err := admit(); err != nil || !decision.Allowed {
		t.Fatalf("seed admission: %+v, %v", decision, err)
	}
	// The existing bucket remains readable while its next write fails. The
	// baseline masked this deterministic database error as ordinary exhaustion.
	if _, err := pool.Exec(ctx, `ALTER TABLE pgbase_meta.api_rate_limits ADD CONSTRAINT injected_quota_write_failure CHECK (requests <= 1)`); err != nil {
		t.Fatal(err)
	}
	decision, err := admit()
	var pgError *pgconn.PgError
	if decision.Allowed || !errors.As(err, &pgError) || pgError.Code != "23514" {
		t.Fatalf("database failure was masked: decision=%+v, err=%v", decision, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE pgbase_meta.api_rate_limits DROP CONSTRAINT injected_quota_write_failure`); err != nil {
		t.Fatal(err)
	}
	if decision, err := admit(); err != nil || !decision.Allowed {
		t.Fatalf("admission after database recovery: %+v, %v", decision, err)
	}
	settings.RateLimits.Rules[0].MaxRequests = 1
	service.Set(settings)
	if decision, err := admit(); err != nil || decision.Allowed || decision.RetryAfter <= 0 {
		t.Fatalf("normal exhaustion lost retry semantics: %+v, %v", decision, err)
	}
}
