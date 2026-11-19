CREATE INDEX admin_password_resets_expires_at_idx ON pgbase_meta.admin_password_resets (expires_at);
CREATE INDEX admin_email_otps_expires_at_idx ON pgbase_meta.admin_email_otps (expires_at);
CREATE INDEX user_email_otps_expires_at_idx ON pgbase_meta.user_email_otps (expires_at);
CREATE INDEX user_sessions_revoked_at_idx ON pgbase_meta.user_sessions (revoked_at) WHERE revoked_at IS NOT NULL;
CREATE INDEX user_email_actions_consumed_at_idx ON pgbase_meta.user_email_actions (consumed_at) WHERE consumed_at IS NOT NULL;
CREATE INDEX user_oauth_states_consumed_at_idx ON pgbase_meta.user_oauth_states (consumed_at) WHERE consumed_at IS NOT NULL;
CREATE INDEX user_mfa_challenges_consumed_at_idx ON pgbase_meta.user_mfa_challenges (consumed_at) WHERE consumed_at IS NOT NULL;
CREATE INDEX user_auth_rate_limits_updated_at_idx ON pgbase_meta.user_auth_rate_limits (updated_at);

CREATE INDEX service_tokens_created_id_idx ON pgbase_meta.service_tokens (created_at DESC, id DESC);
CREATE INDEX service_tokens_live_created_id_idx ON pgbase_meta.service_tokens (created_at DESC, id DESC)
    WHERE active AND revoked_at IS NULL;
