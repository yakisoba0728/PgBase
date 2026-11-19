package userauth

import (
	"context"
	"fmt"
	"time"

	"github.com/yakisoba0728/PgBase/internal/authpolicy"
)

// CleanExpiredCredentials is called by the application's periodic cleaner.
func (s *Service) CleanExpiredCredentials(ctx context.Context) error {
	for _, table := range []string{"user_sessions", "user_email_actions", "user_oauth_states", "user_mfa_challenges", "user_email_otps"} {
		if err := authpolicy.DeleteExpiredRows(ctx, s.pool, table, "expires_at", 0); err != nil {
			return err
		}
	}
	if err := authpolicy.DeleteExpiredRows(ctx, s.pool, "user_sessions", "revoked_at", 15*60); err != nil {
		return err
	}
	for _, table := range []string{"user_email_actions", "user_oauth_states", "user_mfa_challenges"} {
		if err := authpolicy.DeleteExpiredRows(ctx, s.pool, table, "consumed_at", 15*60); err != nil {
			return err
		}
	}
	window := int64(max(30*time.Minute, s.currentRatePolicy().Window).Seconds())
	for {
		tag, err := s.pool.Exec(ctx, `
			with stale as (
				select subject_hash from pgbase_meta.user_auth_rate_limits
				where updated_at <= statement_timestamp() - make_interval(secs=>$1)
				  and (blocked_until is null or blocked_until <= statement_timestamp())
				order by updated_at
				limit 1000 for update skip locked
			)
			delete from pgbase_meta.user_auth_rate_limits as limits
			using stale where limits.subject_hash = stale.subject_hash`, window)
		if err != nil {
			return fmt.Errorf("delete stale user rate limits: %w", err)
		}
		if tag.RowsAffected() < 1000 {
			return nil
		}
	}
}
