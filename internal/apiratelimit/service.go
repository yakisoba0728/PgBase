// Package apiratelimit provides the shared PostgreSQL-backed API quota.
package apiratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// A published policy is immutable. Compile request-independent work at the
// configuration boundary instead of reparsing every excluded network on every
// request. Keeping only the quota settings also avoids retaining unrelated
// runtime configuration in this service.
type policy struct {
	enabled  bool
	rules    []runtimesettings.APIRateRule
	excluded ipExclusions
}

type Service struct {
	pool     *database.Pool
	key      []byte
	settings atomic.Pointer[policy]
	calls    atomic.Uint64
}

func New(pool *database.Pool, key []byte, initial runtimesettings.Settings) *Service {
	s := &Service{pool: pool, key: append([]byte(nil), key...)}
	s.Set(initial)
	return s
}

func (s *Service) Set(v runtimesettings.Settings) {
	s.settings.Store(&policy{
		enabled:  v.RateLimits.Enabled,
		rules:    append([]runtimesettings.APIRateRule(nil), v.RateLimits.Rules...),
		excluded: compileExclusions(v.RateLimits.ExcludedIPs),
	})
}

func (s *Service) Admit(ctx context.Context, method, path, actor, ip string) (Decision, error) {
	v := s.settings.Load()
	if v == nil || !v.enabled || v.excluded.contains(ip) {
		return Decision{Allowed: true}, nil
	}
	audience := "@guest"
	if actor != "" {
		audience = "@auth"
	}
	rule, ok := find(v.rules, []string{method + " " + path, path, "/"}, audience)
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
	// No returned row is the quota predicate's normal refusal. A write error
	// is not exhaustion: preserve it for the caller's fail-closed service error
	// handling rather than masking a database outage with a successful lookup.
	if !errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, err
	}
	var retry float64
	qerr := s.pool.QueryRow(ctx, `SELECT GREATEST(1,EXTRACT(EPOCH FROM window_started_at + ($2 * interval '1 second') - statement_timestamp())) FROM pgbase_meta.api_rate_limits WHERE bucket=$1`, bucket, rule.Duration).Scan(&retry)
	if qerr != nil {
		return Decision{}, errors.Join(err, qerr)
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

type ipExclusions struct {
	addresses map[netip.Addr]struct{}
	prefixes  []netip.Prefix
}

func compileExclusions(values []string) ipExclusions {
	var result ipExclusions
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			result.prefixes = append(result.prefixes, prefix)
		} else if address, err := netip.ParseAddr(value); err == nil {
			if result.addresses == nil {
				result.addresses = make(map[netip.Addr]struct{})
			}
			result.addresses[address] = struct{}{}
		}
	}
	return result
}

func (e ipExclusions) contains(ip string) bool {
	if len(e.addresses) == 0 && len(e.prefixes) == 0 {
		return false
	}
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if _, ok := e.addresses[address]; ok {
		return true
	}
	for _, prefix := range e.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
