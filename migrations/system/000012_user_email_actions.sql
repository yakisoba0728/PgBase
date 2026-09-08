ALTER TABLE pgbase_meta.users
    ADD COLUMN verified boolean NOT NULL DEFAULT false;

CREATE TABLE pgbase_meta.user_email_actions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    purpose text NOT NULL CHECK (purpose IN ('verify', 'password_reset', 'email_change')),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    payload_email text,
    credential_hash bytea NOT NULL CHECK (octet_length(credential_hash) = 32),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    CHECK ((purpose = 'email_change') = (payload_email IS NOT NULL))
);
CREATE INDEX user_email_actions_user_purpose_idx
    ON pgbase_meta.user_email_actions(user_id, purpose, created_at DESC);
CREATE INDEX user_email_actions_expires_at_idx
    ON pgbase_meta.user_email_actions(expires_at);
