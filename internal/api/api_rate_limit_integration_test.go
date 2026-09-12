//go:build integration

package api

import (
	"context"
	"github.com/yakisoba0728/PgBase/internal/apiratelimit"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestAPIRateLimitHTTPRejectsAndHonorsTrustedForwardedClient(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	s := runtimesettings.Default()
	s.RateLimits.Enabled = true
	s.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "/health/live", Duration: 60, MaxRequests: 1}}
	limiter := apiratelimit.New(pool, []byte("01234567890123456789012345678901"), s)
	h := mustRouter(t, Dependencies{APIRateLimits: limiter, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	request := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("203.0.113.9"); w.Code != http.StatusOK {
		t.Fatalf("first=%d", w.Code)
	}
	if w := request("203.0.113.9"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limited=%d retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := request("203.0.113.10"); w.Code != http.StatusOK {
		t.Fatalf("separate forwarded actor=%d", w.Code)
	}
}
