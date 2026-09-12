// Package apiratelimit provides the shared PostgreSQL-backed API quota.
package apiratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"math"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"
)

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}
type Service struct {
	pool     *database.Pool
	key      []byte
	settings atomic.Pointer[runtimesettings.Settings]
	calls    atomic.Uint64
}

func New(pool *database.Pool, key []byte, initial runtimesettings.Settings) *Service {
	s := &Service{pool: pool, key: append([]byte(nil), key...)}
	s.Set(initial)
	return s
}
func (s *Service) Set(v runtimesettings.Settings) {
	v.RateLimits.Rules = append([]runtimesettings.APIRateRule(nil), v.RateLimits.Rules...)
	s.settings.Store(&v)
}
func (s *Service) Admit(ctx context.Context, method, path, actor, ip string) (Decision, error) {
	v := s.settings.Load()
	if v == nil || !v.RateLimits.Enabled || excluded(v.RateLimits.ExcludedIPs, ip) {
		return Decision{Allowed: true}, nil
	}
	audience := "@guest"
	if actor != "" {
		audience = "@auth"
	}
	rule, ok := find(v.RateLimits.Rules, []string{method + " " + path, path, "/"}, audience)
	if !ok {
		return Decision{Allowed: true}, nil
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(rule.Label + "\x00" + rule.Audience + "\x00" + actor + "\x00" + ip))
	bucket := hex.EncodeToString(mac.Sum(nil))
	var started time.Time
	err := s.pool.QueryRow(ctx, `INSERT INTO pgbase_meta.api_rate_limits(bucket,window_started_at,requests,updated_at) VALUES($1,statement_timestamp(),1,statement_timestamp()) ON CONFLICT(bucket) DO UPDATE SET window_started_at=CASE WHEN pgbase_meta.api_rate_limits.window_started_at + ($2 * interval '1 second') <= statement_timestamp() THEN statement_timestamp() ELSE pgbase_meta.api_rate_limits.window_started_at END, requests=CASE WHEN pgbase_meta.api_rate_limits.window_started_at + ($2 * interval '1 second') <= statement_timestamp() THEN 1 ELSE pgbase_meta.api_rate_limits.requests+1 END, updated_at=statement_timestamp() WHERE pgbase_meta.api_rate_limits.window_started_at + ($2 * interval '1 second') <= statement_timestamp() OR pgbase_meta.api_rate_limits.requests < $3 RETURNING window_started_at`, bucket, rule.Duration, rule.MaxRequests).Scan(&started)
	if err == nil {
		if s.calls.Add(1)%128 == 0 {
			_, _ = s.pool.Exec(ctx, `DELETE FROM pgbase_meta.api_rate_limits WHERE bucket IN (SELECT bucket FROM pgbase_meta.api_rate_limits WHERE updated_at < statement_timestamp() - interval '2 days' LIMIT 256)`)
		}
		return Decision{Allowed: true}, nil
	}
	var retry float64
	qerr := s.pool.QueryRow(ctx, `SELECT GREATEST(1,EXTRACT(EPOCH FROM window_started_at + ($2 * interval '1 second') - statement_timestamp())) FROM pgbase_meta.api_rate_limits WHERE bucket=$1`, bucket, rule.Duration).Scan(&retry)
	if qerr != nil {
		return Decision{}, err
	}
	return Decision{Allowed: false, RetryAfter: time.Duration(math.Ceil(retry)) * time.Second}, nil
}
func find(rules []runtimesettings.APIRateRule, labels []string, audience string) (runtimesettings.APIRateRule, bool) {
	for _, l := range labels {
		for _, r := range rules {
			if (r.Audience == "" || r.Audience == audience) && (r.Label == l || (strings.HasSuffix(r.Label, "/") && strings.HasPrefix(l+"/", r.Label))) {
				return r, true
			}
		}
	}
	return runtimesettings.APIRateRule{}, false
}
func excluded(values []string, ip string) bool {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, v := range values {
		if prefix, err := netip.ParsePrefix(v); err == nil && prefix.Contains(address) {
			return true
		}
		if candidate, err := netip.ParseAddr(v); err == nil && candidate == address {
			return true
		}
	}
	return false
}
