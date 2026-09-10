//go:build integration

package apiratelimit

import (
	"context"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresQuotaIsSharedAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	settings := runtimesettings.Default()
	settings.RateLimits.Enabled = true
	settings.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/health/live", Duration: 60, MaxRequests: 1}}
	one, two := New(pool, []byte("01234567890123456789012345678901"), settings), New(pool, []byte("01234567890123456789012345678901"), settings)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for _, service := range []*Service{one, two} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			d, e := s.Admit(ctx, "GET", "/health/live", "", "203.0.113.4")
			if e != nil {
				t.Errorf("Admit: %v", e)
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}(service)
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("allowed=%d, want exactly one distributed quota admission", allowed.Load())
	}
	settings.RateLimits.Enabled = false
	one.Set(settings)
	d, err := one.Admit(ctx, "GET", "/health/live", "", "203.0.113.4")
	if err != nil || !d.Allowed {
		t.Fatalf("runtime disable=%#v err=%v", d, err)
	}
}
